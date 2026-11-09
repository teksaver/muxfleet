# Binaire prj embarqué

`vps` embarque ici le binaire Linux `prj` qu'il pousse sur le serveur (`prj-linux-amd64`). Ce binaire est **généré, jamais commité** (il est ignoré par Git) :

```bash
go generate ./cmd/vps   # compile prj pour linux/amd64 dans ce dossier
go build -trimpath -o bin/vps ./cmd/vps
```

Ce fichier garde le motif `//go:embed embed/*` valide dans un dépôt fraîchement cloné. Un `vps` compilé sans générer `prj` le signale à l'exécution de `push-prj`.
