//go:build !js

package game

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"io/fs"
	"net/http"
	"os"

	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Browser engines download textures from /content/<path>?v=<version>. Only
// files in the manifest are served — never CSVs or scripts — and a versioned
// URL is immutable, so browsers cache every texture until content changes.

// maxServedPictureSide caps pictures and frames on the wire. They only ever
// reach the screen through composePicture's area-averaging (at most a few
// hundred px), so a 4096-px frame is megabytes of download and seconds of
// in-browser PNG decoding for no visible gain.
const maxServedPictureSide = 1024

// contentManifest lists every file a browser engine may need — the textures
// tree and the sprite — and a version hash over their names, sizes and
// modification times.
func contentManifest(root string) (files []string, version string, err error) {
	h := sha256.New()
	add := func(rel string, info fs.FileInfo) {
		files = append(files, rel)
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", rel, info.Size(), info.ModTime().UnixNano())
	}
	err = filepath.WalkDir(filepath.Join(root, "textures"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		add(filepath.ToSlash(rel), info)
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	info, err := os.Stat(filepath.Join(root, spriteFile))
	if err != nil {
		return nil, "", err
	}
	add(spriteFile, info)
	slices.Sort(files)
	return files, hex.EncodeToString(h.Sum(nil))[:16], nil
}

// ContentHandler serves manifest files under the given URL prefix.
func (e *Engine) ContentHandler(prefix string) http.Handler {
	var mu sync.Mutex
	shrunk := map[string][]byte{} // version + path -> downscaled PNG

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, prefix)
		if name, ok := strings.CutPrefix(rel, dovFilePrefix); ok {
			// gallery composites are named by content, so any URL is immutable
			data := e.dov.file(strings.TrimSuffix(name, ".png"))
			if data == nil || !strings.HasSuffix(name, ".png") {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			w.Header().Set("Content-Type", "image/png")
			w.Write(data)
			return
		}
		e.mu.RLock()
		_, known := slices.BinarySearch(e.contentFiles, rel)
		version := e.contentVersion
		e.mu.RUnlock()
		if !known {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join(e.root, filepath.FromSlash(rel)))
		if err != nil {
			http.NotFound(w, r)
			return
		}

		if strings.HasPrefix(rel, "textures/pictures/") || strings.HasPrefix(rel, "textures/frames/") {
			key := version + "\x00" + rel
			mu.Lock()
			small, ok := shrunk[key]
			mu.Unlock()
			if !ok {
				small = shrinkImage(data, maxServedPictureSide)
				mu.Lock()
				shrunk[key] = small
				mu.Unlock()
			}
			data = small
		}

		if r.URL.Query().Get("v") == version {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		w.Header().Set("Content-Type", http.DetectContentType(data))
		w.Write(data)
	})
}

// shrinkImage area-averages an image down so its long side is at most max,
// re-encoded as PNG. Small or undecodable images come back unchanged.
func shrinkImage(data []byte, limit int) []byte {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}
	b := img.Bounds()
	if b.Dx() <= limit && b.Dy() <= limit {
		return data
	}
	w, h := limit, b.Dy()*limit/b.Dx()
	if b.Dy() > b.Dx() {
		w, h = b.Dx()*limit/b.Dy(), limit
	}
	t := textureFromImage(img, true)
	s := resampleArea(t, image.Rect(0, 0, t.W, t.H), max(w, 1), max(h, 1))
	var buf bytes.Buffer
	if err := png.Encode(&buf, &image.NRGBA{Pix: s.Pix, Stride: s.W * 4, Rect: image.Rect(0, 0, s.W, s.H)}); err != nil {
		return data
	}
	return buf.Bytes()
}

// PlayerAvatar returns a connected player's uploaded avatar, or nil.
func (e *Engine) PlayerAvatar(id int) []byte {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, p := range e.players {
		if p.ID == id {
			return p.avatarRaw
		}
	}
	return nil
}
