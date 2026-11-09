# muxfleet

`muxfleet` est un outil d'orchestration et de gestion de postes de code isolés sur serveurs VPS distants.

Il se compose de deux binaires complémentaires :
- **`vps`** : binaire exécuté sur le poste administrateur (macOS, Linux, WSL), assurant le provisionnement sans état local d'infrastructure, l'accès sécurisé via un tunnel WireGuard en espace utilisateur et SSH, la gestion du coffre de clés maîtresses, et la mise à jour atomique de `prj`.
- **`prj`** : binaire exécuté sur le serveur Ubuntu 26.04 LTS sous `root` pour les commandes d'administration système (`system apply`, `system status`, `system record`), et sous l'utilisateur non-privilégié `dev` ou les identités isolées par projet `prj-<nom>` pour le travail de développement interactif et les agents autonomes.

---

## Prérequis pour la démo (sans OpenTofu)

Pour lancer une démo en moins de 15 minutes et tout éteindre à 0 € sans laisser aucune ressource facturée :

1. **Un compte fournisseur VPS avec clé API** :
   - **Scaleway** : une paire d'identifiants API (`Access Key` et `Secret Key`).
   - OU **Hetzner Cloud** : un jeton d'API (`API token`) avec droits de lecture et écriture (*Read & Write*).
   - *Aucun OpenTofu, Terraform ni fichier d'état local n'est requis.*

2. **Pour l'étape d'activation ultérieure (hors démo)** :
   - Un compte de stockage objet S3 distinct chez un second fournisseur (Scaleway Object Storage ou Backblaze B2) pour la sauvegarde chiffrée et la reprise après sinistre.

3. **Pour l'accès mobile facultatif** :
   - L'application WireGuard officielle installée sur votre smartphone (iOS ou Android).
   - L'application Moshi (pour iOS) pour l'accès terminal et les notifications push.

4. **Environnement local** :
   - macOS (Apple Silicon ou Intel), Linux (amd64 ou arm64) ou Windows avec WSL2.
   - La dernière version stable de Go pour compiler depuis les sources (c'est celle de la CI), ou le binaire `vps` précompilé.

---

## Principes d'architecture

- **Architecture hexagonale (AD-1)** : le domaine métier (`internal/domain`) est pur et n'importe aucun SDK externe ni adaptateur. Les parcours (`internal/app`) ne dépendent que des interfaces de ports (`internal/ports`). Les adaptateurs (`internal/adapters`) implémentent ces ports.
- **Dérivation d'état pure (AD-2)** : l'état du serveur (`none`, `demo`, `activating`, `active`, `migrating`, `orphaned_resources`, `cleanup_incomplete`) est dérivé de manière déterministe à partir des preuves réelles, des attestations et des jalons unilatéraux.
- **Contrôle via SDK officiel (AD-6, AD-7)** : pilotage direct des API fournisseurs avec étiquetage strict de chaque ressource (`muxfleet=<serveur>`). Aucun fichier d'état local ne peut mentir ou dériver.
- **Poussée sécurisée de `prj` (AD-5, R9)** : `vps` embarque le binaire Linux `prj`, le dépose via SFTP atomiquement (`/usr/local/bin/prj.tmp` -> vérification SHA256 -> `0755` -> rename atomique vers `/usr/local/bin/prj`), et refuse formellement toute rétrogradation de version.
- **Composants serveur idempotents (AD-22)** : le socle système (`base`, `prj`, `reboot`...) est appliqué par des composants bash idempotents dont le second passage converge systématiquement à 0 modification.

---

## Compilation et tests

```bash
# Générer le binaire prj que vps embarque (Linux amd64, -trimpath). Il n'est jamais commité :
# à relancer avant les tests et avant toute compilation de vps, comme le fait la CI.
go generate ./cmd/vps

# Exécuter l'ensemble des tests unitaires
go test -v ./...

# Toujours compiler avec -trimpath : sans lui, un binaire garde les chemins de la machine qui l'a construit.

# Compiler le binaire serveur Linux (amd64)
GOOS=linux GOARCH=amd64 go build -trimpath -o bin/prj-linux-amd64 ./cmd/prj

# Compiler le binaire poste local (après go generate ./cmd/vps)
go build -trimpath -o bin/vps ./cmd/vps
```

---

## Licence

Apache-2.0
