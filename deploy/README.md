# Déploiement de fragments

`fragments serve` est **un seul binaire** : le SPA Vue est embarqué (`go:embed`),
SQLite est pur Go (sans cgo), aucun service externe. Deux chemins de déploiement
sur un petit VPS — **systemd + Caddy** (recommandé, le plus léger) ou **Docker
compose** (optionnel).

## Construire le binaire Linux

Depuis le poste de dev (le SPA doit être compilé **avant** le binaire) :

```bash
make linux        # → fragments-linux-amd64 (CGO_ENABLED=0, statique)
```

## Configuration (`.env`)

Remplis un `.env` (voir `.env.example` à la racine) avec au minimum :

| Variable | Rôle |
|---|---|
| `FRAGMENTS_PASSWORD` | mot de passe de connexion (**obligatoire**, sauf si OIDC est configuré — voir plus bas) |
| `FRAGMENTS_SECRET` | clé HMAC stable pour signer les sessions (sinon régénérée à chaque démarrage → déconnexions) |
| `FRAGMENTS_SECURE=true` | active le flag `Secure` des cookies (derrière HTTPS) |
| `FRAGMENTS_TRUSTED_PROXIES=127.0.0.1` | **important derrière Caddy** : sans ça, toutes les IP clientes deviennent celle du proxy et le rate-limit de login les regroupe |
| `S3_*` | accès S3 (nécessaires pour cataloguer et pour l'export d'albums) |
| `FRAGMENTS_WORKERS=2` | concurrence du pool (2-4 sur un petit VPS) |
| `FRAGMENTS_FAST_THUMBS=true` | resampler plus léger (ApproxBiLinear) si le CPU du VPS est juste |

## Connexion via OIDC (Pocket ID)

fragments peut déléguer l'authentification à un fournisseur OIDC — pensé pour
[Pocket ID](https://pocket-id.org/). Dès que `FRAGMENTS_OIDC_ISSUER` et
`FRAGMENTS_OIDC_CLIENT_ID` sont définis, **la connexion par mot de passe est
désactivée** et OIDC devient la seule méthode.

1. Dans Pocket ID, crée un client OIDC avec l'URL de callback :
   `https://photos.example.com/api/auth/oidc/callback` (soit
   `<FRAGMENTS_PUBLIC_URL>/api/auth/oidc/callback`). Le flux est Authorization
   Code + **PKCE** : le secret client est facultatif (client public possible).
2. Dans le `.env` (pas de commentaire en fin de ligne : systemd `EnvironmentFile`
   les inclurait dans la valeur) :
   ```bash
   # EXACTEMENT l'issuer de Pocket ID (attention au slash final)
   FRAGMENTS_OIDC_ISSUER=https://id.example.com
   FRAGMENTS_OIDC_CLIENT_ID=...
   # facultatif (vide = client public, PKCE)
   FRAGMENTS_OIDC_CLIENT_SECRET=...
   # le domaine servi par Caddy
   FRAGMENTS_PUBLIC_URL=https://photos.example.com
   # libellé du bouton de connexion
   FRAGMENTS_OIDC_PROVIDER_NAME=Pocket ID
   ```
3. Le contrôle d'accès (qui peut se connecter) se gère **dans Pocket ID** via
   les groupes autorisés du client — fragments fait confiance au fournisseur.

Notes :

- La session fragments reste le cookie signé habituel ; la déconnexion est
  **locale** (la session Pocket ID reste active — comportement SSO normal).
- Si la discovery échoue au démarrage avec « issuer did not match », vérifie le
  slash final de `FRAGMENTS_OIDC_ISSUER` : il doit correspondre exactement à ce
  qu'annonce le fournisseur.
- La vérification des jetons exige une horloge à peu près juste : garde NTP
  actif sur le VPS.

## Chemin 1 — systemd + Caddy (recommandé)

```bash
sudo useradd --system --home /opt/fragments --shell /usr/sbin/nologin fragments
sudo mkdir -p /opt/fragments/data && sudo chown -R fragments:fragments /opt/fragments
sudo install -o fragments -g fragments fragments-linux-amd64 /opt/fragments/fragments
sudo install -o fragments -g fragments -m 600 .env /opt/fragments/.env

sudo cp deploy/fragments.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now fragments
```

Caddy (paquet système) pour le TLS auto — édite le domaine dans `deploy/Caddyfile` :

```bash
sudo cp deploy/Caddyfile /etc/caddy/Caddyfile   # remplace photos.example.com
sudo systemctl reload caddy
```

Le serveur écoute sur `127.0.0.1:8080`, Caddy termine HTTPS et proxifie (avec
`flush_interval -1` pour que le flux SSE du statut live passe sans tampon).

## Chemin 2 — Docker compose (optionnel)

```bash
# remplir ../.env et le domaine dans ./Caddyfile
cd deploy && docker compose up -d --build
```

Image multi-stage (node build → go build → **distroless static**). Volume
`fragments-data` = `catalog.db` + miniatures, persistant entre les rebuilds.

## Données, sauvegardes, DR

- **Miniatures** : `data/thumbs/` — volume persistant **distinct** de la DB. Les
  régénérer = un `GetObject` plein-res + décodage + resize **par photo** (coûteux
  en egress S3) ; c'est la reprise la plus chère.
- **Sauvegarde DB** (copie cohérente, WAL-safe, pur Go) :
  ```bash
  /opt/fragments/fragments backup -data /opt/fragments/data /backups/catalog-$(date +%F).db
  ```
  Mets-la dans un cron quotidien. Sauvegarde aussi `data/thumbs/` (rsync/tar).
- **Optionnel** : Litestream (réplication continue du WAL vers un bucket
  **séparé** du bucket photos) ; sync `data/thumbs/` → S3 pour éviter la
  régénération.

## Checklist sécurité avant exposition

- [ ] `FRAGMENTS_PASSWORD` fort + `FRAGMENTS_SECRET` long et stable.
- [ ] `FRAGMENTS_SECURE=true` (cookies derrière HTTPS).
- [ ] OIDC : issuer exact (slash final !), `FRAGMENTS_PUBLIC_URL` = domaine
      Caddy, callback enregistré dans Pocket ID, accès restreint aux bons
      groupes côté Pocket ID.
- [ ] `FRAGMENTS_TRUSTED_PROXIES=127.0.0.1` (ou le CIDR du proxy).
- [ ] `.env` en `chmod 600`, propriétaire `fragments`.
- [ ] Domaine pointant sur le VPS, Caddy a bien obtenu le certificat.
