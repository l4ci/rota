package fsio

import "os"

// WriteTemp writes content to a closed temporary file in dir. Callers supply
// dir through their environment seam (empty uses the OS default) and defer
// cleanup until the command consuming the path returns. On a write or close
// error the incomplete file is removed before returning.
func WriteTemp(dir, pattern string, content []byte) (path string, cleanup func(), err error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", func() {}, err
	}
	cleanup = func() { os.Remove(f.Name()) }
	_, err = f.Write(content)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	return f.Name(), cleanup, nil
}
