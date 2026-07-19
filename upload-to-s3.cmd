@echo off
setlocal
rem ==========================================================================
rem  Sauvegarde des photos/videos vers un stockage objet compatible S3
rem  via rclone. Version Windows (.cmd) de upload-to-s3.sh.
rem
rem  Mode "copy" : ajoute les nouveaux fichiers sur le bucket et NE SUPPRIME
rem  JAMAIS rien. L'operation est incrementale (rclone saute ce qui est deja
rem  uploade).
rem
rem  La configuration est lue depuis le fichier .env a cote du script (comme
rem  upload-to-s3.sh) ; une variable deja definie dans l'environnement garde la
rem  priorite. Le remote rclone est defini "a la volee" via des variables
rem  d'env, donc aucun secret n'est stocke dans rclone.conf.
rem
rem  Variables OBLIGATOIRES (.env ou environnement) :
rem    S3_ACCESS_KEY_ID        cle d'acces S3
rem    S3_SECRET_ACCESS_KEY    cle secrete S3
rem    S3_BUCKET               bucket cible
rem    S3_ENDPOINT             endpoint du fournisseur
rem
rem  Autres variables (facultatives) :
rem    S3_REGION=<vide>
rem    S3_ACL=private
rem    SRC_DIR=<dossier du script>
rem    DEST_PREFIX=<vide>
rem
rem  Usage :
rem    upload-to-s3.cmd                 upload (copy)
rem    upload-to-s3.cmd --dry-run       simulation, ne transfere rien
rem    upload-to-s3.cmd ls              liste les fichiers sur le bucket
rem    upload-to-s3.cmd size            taille totale sur le bucket
rem    upload-to-s3.cmd check           verifie local ^<-^> distant
rem ==========================================================================

rem --- Dossier du script (= dossier sauvegarde par defaut) ------------------
set "SCRIPT_DIR=%~dp0"
if "%SCRIPT_DIR:~-1%"=="\" set "SCRIPT_DIR=%SCRIPT_DIR:~0,-1%"

rem --- Chargement du .env (avant enabledelayedexpansion pour preserver les
rem     eventuels "!" dans les valeurs) ; l'environnement garde la priorite. ---
if exist "%SCRIPT_DIR%\.env" (
  for /f "usebackq eol=# tokens=1,* delims==" %%a in ("%SCRIPT_DIR%\.env") do (
    if not defined %%a set "%%a=%%b"
  )
)
setlocal enabledelayedexpansion

rem --- Verifications --------------------------------------------------------
where rclone >nul 2>nul
if errorlevel 1 (
  echo [X] rclone n'est pas installe. 1>&2
  exit /b 1
)
if not defined S3_ACCESS_KEY_ID (
  echo [X] Variable S3_ACCESS_KEY_ID manquante ^(.env ou environnement^) 1>&2
  exit /b 1
)
if not defined S3_SECRET_ACCESS_KEY (
  echo [X] Variable S3_SECRET_ACCESS_KEY manquante ^(.env ou environnement^) 1>&2
  exit /b 1
)
if not defined S3_BUCKET (
  echo [X] Variable S3_BUCKET manquante ^(.env ou environnement^) 1>&2
  exit /b 1
)
if not defined S3_ENDPOINT (
  echo [X] Variable S3_ENDPOINT manquante ^(.env ou environnement^) 1>&2
  exit /b 1
)

rem --- Configuration (valeurs par defaut) -----------------------------------
if not defined S3_ACL set "S3_ACL=private"
if not defined SRC_DIR set "SRC_DIR=%SCRIPT_DIR%"

rem --- Remote rclone "a la volee" via variables d'environnement -------------
rem (le nom du remote est "s3" ; ces variables le configurent sans rclone.conf)
set "RCLONE_CONFIG_S3_TYPE=s3"
set "RCLONE_CONFIG_S3_PROVIDER=Other"
set "RCLONE_CONFIG_S3_ACCESS_KEY_ID=%S3_ACCESS_KEY_ID%"
set "RCLONE_CONFIG_S3_SECRET_ACCESS_KEY=%S3_SECRET_ACCESS_KEY%"
set "RCLONE_CONFIG_S3_ENDPOINT=%S3_ENDPOINT%"
set "RCLONE_CONFIG_S3_REGION=%S3_REGION%"
set "RCLONE_CONFIG_S3_ACL=%S3_ACL%"

rem --- Destination ----------------------------------------------------------
set "DEST=s3:%S3_BUCKET%"
if defined DEST_PREFIX set "DEST=s3:%S3_BUCKET%/%DEST_PREFIX%"

rem Fichiers a inclure (majuscules ET minuscules)
set "INCLUDE=*.{JPG,jpg,JPEG,jpeg,RAF,raf,NEF,nef,CR2,cr2,CR3,cr3,ARW,arw,DNG,dng,ORF,orf,RW2,rw2,MOV,mov,MP4,mp4}"

rem --- Commandes de lecture (pratique pour inspecter le bucket) -------------
set "VERB=%~1"
if /i "%VERB%"=="ls"    goto read
if /i "%VERB%"=="lsl"   goto read
if /i "%VERB%"=="lsd"   goto read
if /i "%VERB%"=="size"  goto read
if /i "%VERB%"=="tree"  goto read
if /i "%VERB%"=="check" goto read
goto upload

:read
rem consomme le verbe puis reconstitue le reste des arguments
shift
set "REST="
:read_loop
if "%~1"=="" goto read_run
set "REST=!REST! %1"
shift
goto read_loop
:read_run
if /i "%VERB%"=="check" (
  rclone check "%SRC_DIR%" "%DEST%" --include "%INCLUDE%"!REST!
  exit /b !errorlevel!
)
rclone %VERB% "%DEST%"!REST!
exit /b !errorlevel!

:upload
echo Source      : %SRC_DIR%
echo Destination : %DEST%
echo Endpoint    : %S3_ENDPOINT% (region %S3_REGION%)
echo.

rem Cree le bucket s'il n'existe pas (sans echouer s'il existe deja)
rclone mkdir "s3:%S3_BUCKET%" >nul 2>nul

rclone copy "%SRC_DIR%" "%DEST%" ^
  --include "%INCLUDE%" ^
  --transfers 4 ^
  --checkers 8 ^
  --fast-list ^
  --progress ^
  --stats 10s ^
  --stats-one-line %*

set "RC=!errorlevel!"
echo.
if "%RC%"=="0" (echo Termine.) else (echo [X] Echec ^(code %RC%^).)
exit /b %RC%
