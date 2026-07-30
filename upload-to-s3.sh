#!/usr/bin/env bash
#
# Sauvegarde des photos/vidéos vers un stockage objet compatible S3 via rclone.
#
# Mode "copy" : ajoute les nouveaux fichiers sur le bucket et NE SUPPRIME JAMAIS
# rien. L'opération est incrémentale (rclone saute ce qui est déjà uploadé).
#
# Le remote rclone est défini "à la volée" via des variables d'environnement,
# alimentées par le fichier .env (donc aucun secret dans rclone.conf).
#
# Usage :
#   ./upload-to-s3.sh                 # upload (copy)
#   ./upload-to-s3.sh --dry-run       # simulation, ne transfère rien
#   ./upload-to-s3.sh ls              # liste les fichiers sur le bucket
#   ./upload-to-s3.sh size            # taille totale sur le bucket
#   ./upload-to-s3.sh check           # vérifie local <-> distant
#
set -euo pipefail

# --- Dossier du script (= dossier sauvegardé par défaut) ------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# --- Chargement de la configuration (.env) --------------------------------
ENV_FILE="${ENV_FILE:-$SCRIPT_DIR/.env}"
if [[ ! -f "$ENV_FILE" ]]; then
  echo "❌ Fichier de configuration introuvable : $ENV_FILE" >&2
  echo "   Crée-le à partir du modèle :  cp .env.example .env  puis remplis-le." >&2
  exit 1
fi
set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

# --- Vérifications --------------------------------------------------------
command -v rclone >/dev/null 2>&1 || { echo "❌ rclone n'est pas installé." >&2; exit 1; }
: "${S3_ACCESS_KEY_ID:?Variable S3_ACCESS_KEY_ID manquante dans .env}"
: "${S3_SECRET_ACCESS_KEY:?Variable S3_SECRET_ACCESS_KEY manquante dans .env}"
: "${S3_BUCKET:?Variable S3_BUCKET manquante dans .env}"
: "${S3_ENDPOINT:?Variable S3_ENDPOINT manquante dans .env}"

# Garde-fou : sans SRC_DIR explicite, si le script vit dans un dépôt git (un
# clone du projet), on refuserait d'« uploader » le code source vers le bucket.
if [[ -z "${SRC_DIR:-}" && -e "$SCRIPT_DIR/.git" ]]; then
  echo "❌ SRC_DIR n'est pas défini et le script est dans un dépôt git." >&2
  echo "   Indique ton dossier de photos :  SRC_DIR=/chemin/vers/DCIM $0" >&2
  exit 1
fi
SRC_DIR="${SRC_DIR:-$SCRIPT_DIR}"
S3_REGION="${S3_REGION:-}"
DEST_PREFIX="${DEST_PREFIX:-}"

# --- Remote rclone "à la volée" via variables d'environnement -------------
# (le nom du remote est "s3" ; ces variables le configurent sans rclone.conf)
export RCLONE_CONFIG_S3_TYPE=s3
export RCLONE_CONFIG_S3_PROVIDER=Other
export RCLONE_CONFIG_S3_ACCESS_KEY_ID="$S3_ACCESS_KEY_ID"
export RCLONE_CONFIG_S3_SECRET_ACCESS_KEY="$S3_SECRET_ACCESS_KEY"
export RCLONE_CONFIG_S3_ENDPOINT="$S3_ENDPOINT"
export RCLONE_CONFIG_S3_REGION="$S3_REGION"
export RCLONE_CONFIG_S3_ACL="${S3_ACL:-private}"

DEST="s3:${S3_BUCKET}"
[[ -n "$DEST_PREFIX" ]] && DEST="s3:${S3_BUCKET}/${DEST_PREFIX}"

# Fichiers à inclure (majuscules ET minuscules)
INCLUDE='*.{JPG,jpg,JPEG,jpeg,RAF,raf,NEF,nef,CR2,cr2,CR3,cr3,ARW,arw,DNG,dng,ORF,orf,RW2,rw2,MOV,mov,MP4,mp4}'

# --- Commandes de lecture (pratique pour inspecter le bucket) -------------
case "${1:-}" in
  ls|lsl|lsd|size|tree|check)
    VERB="$1"; shift
    if [[ "$VERB" == "check" ]]; then
      exec rclone check "$SRC_DIR" "$DEST" --include "$INCLUDE" "$@"
    fi
    exec rclone "$VERB" "$DEST" "$@"
    ;;
esac

# --- Upload (copy) --------------------------------------------------------
echo "📤 Source      : $SRC_DIR"
echo "🪣 Destination : $DEST"
echo "🌐 Endpoint    : $S3_ENDPOINT (région ${S3_REGION:-auto})"
echo

# Crée le bucket s'il n'existe pas (sans échouer s'il existe déjà)
rclone mkdir "s3:${S3_BUCKET}" >/dev/null 2>&1 || true

rclone copy "$SRC_DIR" "$DEST" \
  --include "$INCLUDE" \
  --transfers 4 \
  --checkers 8 \
  --fast-list \
  --progress \
  --stats 10s \
  --stats-one-line \
  "$@"

echo
echo "✅ Terminé."
