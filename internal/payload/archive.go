package payload

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"io"
	"io/fs"
	"time"
)

// Extract authenticates the first two entries before accepting any executable
// bytes. Archives contain regular files only, in manifest order; directory
// entries, links, alternate names, duplicates and unlisted files are rejected.
func Extract(data []byte, format string, key ed25519.PublicKey) (Bundle, error) {
	var b Bundle
	if len(data) == 0 || len(data) > MaxArchive {
		return b, ErrInvalid
	}
	index := 0
	accept := func(name string, mode fs.FileMode, size int64, r io.Reader) error {
		if !mode.IsRegular() || !ValidPath(name) {
			return ErrInvalid
		}
		limit := int64(MaxFile)
		wantName := ManifestName
		wantMode := uint32(0644)
		switch index {
		case 0:
			limit = MaxManifest
		case 1:
			wantName = SignatureName
			limit = ed25519.SignatureSize
		default:
			if index-2 >= len(b.Manifest.Files) {
				return ErrInvalid
			}
			f := b.Manifest.Files[index-2]
			wantName = f.Path
			wantMode = f.Mode
			limit = f.Size
			if size != f.Size {
				return ErrInvalid
			}
		}
		if name != wantName || uint32(mode.Perm()) != wantMode || mode&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 || size < 0 || size > limit {
			return ErrInvalid
		}
		value, err := io.ReadAll(io.LimitReader(r, limit+1))
		if err != nil || int64(len(value)) != size {
			return ErrInvalid
		}
		switch index {
		case 0:
			b.Encoded = value
		case 1:
			b.Signature = value
			b.Manifest, err = Authenticate(b.Encoded, b.Signature, key)
			if err != nil {
				return err
			}
			b.Files = map[string][]byte{}
		default:
			if Digest(value) != b.Manifest.Files[index-2].SHA256 {
				return ErrInvalid
			}
			b.Files[name] = value
		}
		index++
		return nil
	}
	switch format {
	case "zip":
		r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil || len(r.File) > MaxFiles+2 {
			return Bundle{}, ErrInvalid
		}
		for _, f := range r.File {
			if f.UncompressedSize64 > MaxFile {
				return Bundle{}, ErrInvalid
			}
			r, err := f.Open()
			if err != nil {
				return Bundle{}, ErrInvalid
			}
			err = accept(f.Name, f.Mode(), int64(f.UncompressedSize64), r)
			closeErr := r.Close()
			if err != nil || closeErr != nil {
				return Bundle{}, ErrInvalid
			}
		}
	case "tar.gz":
		source := bytes.NewReader(data)
		gz, err := gzip.NewReader(source)
		if err != nil {
			return Bundle{}, ErrInvalid
		}
		defer gz.Close()
		gz.Multistream(false)
		limited := &io.LimitedReader{R: gz, N: MaxExpanded + MaxManifest + (MaxFiles+2)*4096}
		r := tar.NewReader(limited)
		for {
			h, err := r.Next()
			if err == io.EOF {
				break
			}
			if err != nil || h.Typeflag != tar.TypeReg || h.Linkname != "" || index > MaxFiles+1 || len(h.Xattrs) != 0 {
				return Bundle{}, ErrInvalid
			}
			for k := range h.PAXRecords {
				if k != "path" {
					return Bundle{}, ErrInvalid
				}
			}
			if err := accept(h.Name, h.FileInfo().Mode(), h.Size, r); err != nil {
				return Bundle{}, err
			}
		}
		// Validate the gzip checksum and reject extra streams or non-padding data.
		tail, err := io.ReadAll(io.LimitReader(limited, 1025))
		if err != nil || len(tail) > 1024 || source.Len() != 0 || limited.N <= 0 {
			return Bundle{}, ErrInvalid
		}
		for _, c := range tail {
			if c != 0 {
				return Bundle{}, ErrInvalid
			}
		}
	default:
		return Bundle{}, ErrInvalid
	}
	if index != len(b.Manifest.Files)+2 || len(b.Files) == 0 {
		return Bundle{}, ErrInvalid
	}
	return b, nil
}

// Archive writes reproducible archives from a sealed, internally consistent unit.
func Archive(b Bundle, format string, builtAt time.Time) ([]byte, error) {
	m, err := Decode(b.Encoded)
	if err != nil || len(b.Signature) != ed25519.SignatureSize || len(b.Files) != len(m.Files) {
		return nil, ErrInvalid
	}
	entries := append([]File{{Path: ManifestName, Mode: 0644}, {Path: SignatureName, Mode: 0644}}, m.Files...)
	value := func(f File) ([]byte, error) {
		if f.Path == ManifestName {
			return b.Encoded, nil
		}
		if f.Path == SignatureName {
			return b.Signature, nil
		}
		v, ok := b.Files[f.Path]
		if !ok || int64(len(v)) != f.Size || Digest(v) != f.SHA256 {
			return nil, ErrInvalid
		}
		return v, nil
	}
	var out bytes.Buffer
	switch format {
	case "zip":
		w := zip.NewWriter(&out)
		for _, f := range entries {
			v, err := value(f)
			if err != nil {
				return nil, err
			}
			h := &zip.FileHeader{Name: f.Path, Method: zip.Deflate}
			h.SetMode(fs.FileMode(f.Mode))
			h.SetModTime(builtAt)
			dst, err := w.CreateHeader(h)
			if err != nil {
				return nil, err
			}
			if _, err = dst.Write(v); err != nil {
				return nil, err
			}
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
	case "tar.gz":
		gz := gzip.NewWriter(&out)
		gz.Header.ModTime = builtAt
		gz.Header.OS = 255
		w := tar.NewWriter(gz)
		for _, f := range entries {
			v, err := value(f)
			if err != nil {
				return nil, err
			}
			h := &tar.Header{Name: f.Path, Mode: int64(f.Mode), Size: int64(len(v)), Typeflag: tar.TypeReg, ModTime: builtAt}
			if err = w.WriteHeader(h); err != nil {
				return nil, err
			}
			if _, err = w.Write(v); err != nil {
				return nil, err
			}
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		if err := gz.Close(); err != nil {
			return nil, err
		}
	default:
		return nil, ErrInvalid
	}
	if out.Len() > MaxArchive {
		return nil, ErrInvalid
	}
	return out.Bytes(), nil
}
