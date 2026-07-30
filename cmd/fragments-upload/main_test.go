package main

import (
	"flag"
	"reflect"
	"testing"
)

// newTestFlagSet mirrors the flags run() registers.
func newTestFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("env", ".env", "")
	fs.String("src", "", "")
	fs.Bool("dry-run", false, "")
	fs.Bool("yes", false, "")
	fs.Bool("list", false, "")
	fs.Bool("version", false, "")
	return fs
}

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantOwn  []string
		wantRest []string
	}{
		{"empty", nil, nil, nil},
		{"own flags only",
			[]string{"-src", "/x", "-dry-run"},
			[]string{"-src", "/x", "-dry-run"}, nil},
		{"own flag with equals",
			[]string{"-env=prod.env", "-yes"},
			[]string{"-env=prod.env", "-yes"}, nil},
		{"rclone flag passes through",
			[]string{"-yes", "--bwlimit", "1M"},
			[]string{"-yes"}, []string{"--bwlimit", "1M"}},
		{"rclone flag first stops claiming",
			[]string{"--transfers", "8", "-yes"},
			nil, []string{"--transfers", "8", "-yes"}},
		{"verb stops claiming",
			[]string{"-env", "prod.env", "ls", "--max-depth", "2"},
			[]string{"-env", "prod.env"}, []string{"ls", "--max-depth", "2"}},
		{"double dash forces the cut",
			[]string{"-yes", "--", "-src", "weird"},
			[]string{"-yes"}, []string{"-src", "weird"}},
		{"bool flag does not eat the next token",
			[]string{"-dry-run", "check"},
			[]string{"-dry-run"}, []string{"check"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			own, rest := splitArgs(newTestFlagSet(), c.args)
			if !reflect.DeepEqual(own, c.wantOwn) || !reflect.DeepEqual(rest, c.wantRest) {
				t.Errorf("splitArgs(%v) = own %v, rest %v; want own %v, rest %v",
					c.args, own, rest, c.wantOwn, c.wantRest)
			}
		})
	}
}
