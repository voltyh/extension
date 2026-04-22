package zipper

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func CreateFromDir(srcDir, outZip string) error {
	if err := os.MkdirAll(filepath.Dir(outZip), 0o755); err != nil {
		return err
	}
	f, err := os.Create(outZip)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	var files []string
	err = filepath.WalkDir(srcDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)

	fixed := time.Unix(0, 0).UTC()
	for _, rel := range files {
		abs := filepath.Join(srcDir, filepath.FromSlash(rel))
		in, err := os.Open(abs)
		if err != nil {
			return err
		}

		h := &zip.FileHeader{
			Name:   rel,
			Method: zip.Deflate,
		}
		h.SetMode(0o644)
		h.SetModTime(fixed)

		w, err := zw.CreateHeader(h)
		if err != nil {
			in.Close()
			return err
		}
		if _, err := io.Copy(w, in); err != nil {
			in.Close()
			return err
		}
		in.Close()
	}
	return nil
}
