# fragments — build the embedded single binary (frontend first, then Go).
#
# The Go binary embeds web/dist via go:embed, so the SPA must be built BEFORE the
# Go build. CGO_ENABLED=0 keeps the binary pure-Go / static (modernc sqlite, gin,
# image libs are all cgo-free) so it cross-compiles and runs on a bare VPS.
.PHONY: build ui server linux serve dev tidy clean

build: ui server

# vite empties dist/ (removing the committed .gitkeep placeholder); restore it
# so a build never leaves the working tree dirty.
ui:
	cd web && npm install && npm run build && touch dist/.gitkeep

server:
	CGO_ENABLED=0 go build -o fragments ./cmd/fragments

# Static linux/amd64 binary for the VPS (run AFTER `make ui`).
linux: ui
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o fragments-linux-amd64 ./cmd/fragments

# Run the server over an existing ./data catalog (needs FRAGMENTS_PASSWORD set).
serve:
	go run ./cmd/fragments serve

# Frontend dev server on :5173, proxying to a running `fragments serve` (:8088).
dev:
	cd web && npm run dev

tidy:
	go mod tidy

clean:
	rm -f fragments fragments.exe fragments-linux-amd64
	rm -rf web/dist/assets web/dist/index.html
