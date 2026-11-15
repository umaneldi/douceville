// Package web embarque les fichiers statiques du frontend (HTML/CSS/JS) dans le
// binaire, afin que l'image Docker unique serve à la fois l'API et l'interface.
package web

import "embed"

//go:embed index.html app.css app.js
var Files embed.FS
