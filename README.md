# fragments

*Vos photos dorment dans un bucket. Réveillez les meilleures.*

**fragments** est un petit outil auto-hébergé pour trier ses photos à tête
reposée. Le principe :

1. **Sauvegardez** votre carte SD vers un stockage objet S3 avec
   [rclone](https://rclone.org/) (un script prêt à l'emploi est fourni) — vos
   originaux sont à l'abri, RAW compris.
2. **Cataloguez** le bucket avec `fragments scan` : l'outil apparie chaque JPEG
   avec son RAW, extrait les métadonnées EXIF, génère une miniature et range le
   tout dans une base SQLite locale. Les RAW ne sont **jamais téléchargés**,
   seulement référencés.
3. **Notez** vos photos dans l'interface web (`fragments serve`) : galerie
   fluide, lightbox, étoiles, corbeille, albums, export ZIP. Un soir de pluie,
   un thé, et la sélection se fait toute seule.

Le tout tient dans **un seul binaire** (la SPA Vue est embarquée dedans), sans
service externe, et tourne très bien sur un mini-VPS.

> Fonctionne avec n'importe quel stockage compatible S3 (OVH, AWS, Scaleway,
> MinIO…) et n'importe quel appareil produisant des JPEG (+ RAW en option).
> Le projet est développé et testé avec un **Fujifilm X-T30 II** et **OVH
> Object Storage** — les simulations de film Fujifilm sont d'ailleurs décodées
> et affichées quand elles existent.

## Ce que ça fait

- **Appariement JPEG + RAW** : `DSCF0960.JPG` + `DSCF0960.RAF` → une seule
  photo (RAF, NEF, CR2/CR3, ARW, DNG, ORF, RW2… reconnus).
- **Catalogage frugal** : seul le JPEG est téléchargé, une fois. Le passage
  suivant saute tout ce qui n'a pas changé (comparaison d'ETag).
- **EXIF complet** : boîtier, objectif, ISO, ouverture, vitesse… plus le dump
  intégral consultable dans l'interface, et la simulation de film pour les
  Fujifilm.
- **Galerie web** : mise en page justifiée, lightbox plein écran avec
  raccourcis clavier, notation par étoiles, mise à la corbeille, albums,
  export ZIP (avec ou sans les RAW).
- **Un binaire, zéro dépendance** : Go pur (pas de cgo, pas d'exiftool ni
  d'ImageMagick), SQLite embarqué, SPA incluse via `go:embed`.

## Démarrage rapide

Prérequis : Go 1.26+, Node 22+ (pour compiler la SPA), et un bucket S3 quelque
part.

```bash
# 0. Configurer
cp .env.example .env        # puis remplir clés S3 + mot de passe web

# 1. Sauvegarder la carte SD vers le bucket (incrémental, ne supprime jamais)
SRC_DIR=/chemin/vers/DCIM ./upload-to-s3.sh    # Windows : upload-to-s3.cmd

# 2. Cataloguer le bucket (test possible : -prefix 100_FUJI/ -limit 10)
go run ./cmd/fragments scan

# 3. Compiler puis lancer l'interface web
make build                  # ou .\build.ps1 sous Windows
./fragments serve           # http://localhost:8080
```

Envie d'essayer sans bucket ? `fragments scan -local ./mon-dossier` catalogue
un dossier local de JPEG, hors ligne.

## Configuration

Tout passe par le fichier `.env` (voir [.env.example](.env.example), qui
documente chaque variable) :

| Variable | Rôle |
|---|---|
| `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | identifiants S3 |
| `S3_BUCKET` | bucket cible |
| `S3_REGION` / `S3_ENDPOINT` | région et endpoint du fournisseur |
| `S3_FORCE_PATH_STYLE` | `true` pour MinIO et consorts |
| `FRAGMENTS_PASSWORD` | mot de passe de l'interface web (**obligatoire**) |
| `FRAGMENTS_SECRET` | clé de signature des sessions (longue et stable) |

## L'interface web

`fragments serve` n'écoute que sur `127.0.0.1` par défaut. `-network` l'expose
sur le LAN (pratique pour trier depuis le canapé, tablette à la main) et
affiche les URLs joignables. Derrière un vrai domaine, voir
[deploy/](deploy/README.md) — systemd + Caddy ou Docker compose, checklist
sécurité incluse.

Pour développer le frontend avec rechargement à chaud :

```bash
./fragments serve -addr :8088     # backend
cd web && npm run dev             # http://localhost:5173 (proxy → :8088)
```

## Sous le capot

- **Go + Gin** côté serveur, **Vue 3 + PhotoSwipe** côté galerie.
- **EXIF** : `dsoprea/go-exif` ; la simulation de film Fujifilm est décodée à
  la main depuis la maker note (aucune lib Go ne la nomme).
- **SQLite** : `modernc.org/sqlite` (pur Go). Le re-catalogage n'écrase
  **jamais** vos notes ni vos décisions.
- **Miniatures** : `golang.org/x/image` (Catmull-Rom), orientation EXIF
  appliquée.
- **S3** : AWS SDK v2 pointé sur l'endpoint de votre choix.

## Licence

[MIT](LICENSE) — servez-vous.
