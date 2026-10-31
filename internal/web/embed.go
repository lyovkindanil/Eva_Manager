// Package web встраивает собранный интерфейс (web/ → internal/web/dist)
// в бинарник панели. Без сборки интерфейса (npm run build) в dist только
// .gitkeep, и панель показывает страницу-заглушку.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS — файлы интерфейса: index.html, assets/…
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// Built — собран ли интерфейс.
func Built(fsys fs.FS) bool {
	_, err := fs.Stat(fsys, "index.html")
	return err == nil
}
