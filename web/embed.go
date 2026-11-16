// Package web embarque les fichiers statiques du frontend (HTML/CSS/JS) dans le
// binaire, afin que l'image Docker unique serve à la fois l'API et l'interface.
package web

import "embed"

//go:embed index.html app.css app.js
var Files embed.FS

// Version est la version de l'application affichée dans le titre de l'interface.
// Elle est injectée à la compilation :
//
//	go build -ldflags "-X github.com/ydethe/douceville/web.Version=v1.2.3" ./cmd/server
var Version = "dev"
