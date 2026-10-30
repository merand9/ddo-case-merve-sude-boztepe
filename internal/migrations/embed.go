// Package migrations SQL şema dosyalarını ikili dosyaya gömer.
package migrations

import "embed"

// FS sıralı migrasyon dosyalarını içerir (NNNN_ad.sql).
//
//go:embed *.sql
var FS embed.FS
