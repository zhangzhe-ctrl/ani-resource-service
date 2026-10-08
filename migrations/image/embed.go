// Package imagemigrations owns only the Image schema migration stream.
package imagemigrations

import "embed"

//go:embed *.sql
var Files embed.FS
