// Package httpx contains the shared HTTP client and a download helper
// with optional progress output.
package httpx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"winget-go/internal/config"
)

// Client is the shared HTTP client.
var Client = &http.Client{Timeout: 0, Transport: http.DefaultTransport}

// UserAgent identifies this client to servers.
var UserAgent = "winget-go/" + config.Version

// NewRequest builds a GET request with the standard headers.
func NewRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	return req, nil
}

// GetBytes fetches a URL into memory (for small documents).
func GetBytes(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := NewRequest(ctx, url)
	if err != nil {
		return nil, err
	}
	resp, err := Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}

// Download streams url into dst (via a temp file renamed on success) and
// returns the lowercase hex SHA-256 of the content. Progress is written
// to progress (if non-nil).
func Download(ctx context.Context, url, dst string, progress io.Writer) (string, error) {
	req, err := NewRequest(ctx, url)
	if err != nil {
		return "", err
	}
	resp, err := Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".download-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	var r io.Reader = resp.Body
	if progress != nil {
		r = &progressReader{r: resp.Body, total: resp.ContentLength, w: progress}
	}
	_, err = io.Copy(io.MultiWriter(tmp, h), r)
	if progress != nil {
		fmt.Fprintln(progress)
	}
	if err == nil {
		err = tmp.Chmod(0o644)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FileSHA256 hashes a file.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type progressReader struct {
	r     io.Reader
	total int64
	done  int64
	last  time.Time
	w     io.Writer
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if time.Since(p.last) > 100*time.Millisecond || err == io.EOF {
		p.last = time.Now()
		if p.total > 0 {
			const width = 30
			fill := int(float64(width) * float64(p.done) / float64(p.total))
			bar := make([]byte, width)
			for i := range bar {
				if i < fill {
					bar[i] = '#'
				} else {
					bar[i] = '-'
				}
			}
			fmt.Fprintf(p.w, "\r  [%s] %s / %s", bar, human(p.done), human(p.total))
		} else {
			fmt.Fprintf(p.w, "\r  %s", human(p.done))
		}
	}
	return n, err
}

func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
