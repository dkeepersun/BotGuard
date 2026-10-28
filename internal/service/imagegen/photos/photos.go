// Package photos embeds the built-in background photos: CC0 (public domain)
// landscapes from Wikimedia Commons. photos.json records where each came
// from; tools/photolib rebuilds the set from that list.
//
// The set is public, so it makes pictures look natural rather than keeping
// anything secret; see imagegen's treat for what each challenge changes.
package photos

import (
	"embed"
	"sync"

	"github.com/dkeepersun/BotGuard/internal/service/imagegen"
)

//go:embed *.jpg
var files embed.FS

// Library returns the built-in photos, in file order.
var Library = sync.OnceValue(func() *imagegen.Library {
	entries, err := files.ReadDir(".")
	if err != nil {
		panic(err)
	}
	jpegs := make([][]byte, 0, len(entries))
	for _, e := range entries {
		b, err := files.ReadFile(e.Name())
		if err != nil {
			panic(err)
		}
		jpegs = append(jpegs, b)
	}
	return imagegen.NewLibrary(jpegs)
})
