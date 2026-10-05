package engine

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

type probeInfo struct {
	size     int64 // -1 when unknown
	rangeOK  bool
	finalURL string
	etag     string
	lastMod  string
	name     string
}

const userAgent = "MultiNet-Downloader/1.0"

func newRequest(ctx context.Context, u string, h http.Header) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range h {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", userAgent)
	}
	req.Header.Set("Accept-Encoding", "identity")
	return req, nil
}

// probe asks for byte 0 only. 206 => ranges work and Content-Range has the size.
func probe(ctx context.Context, c *http.Client, rawURL string, h http.Header) (*probeInfo, error) {
	req, err := newRequest(ctx, rawURL, h)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	defer io.CopyN(io.Discard, resp.Body, 64<<10)

	info := &probeInfo{size: -1, finalURL: resp.Request.URL.String()}
	switch resp.StatusCode {
	case http.StatusPartialContent:
		if total, ok := parseContentRangeTotal(resp.Header.Get("Content-Range")); ok {
			info.size = total
			info.rangeOK = true
		}
	case http.StatusOK:
		info.size = resp.ContentLength
	case http.StatusRequestedRangeNotSatisfiable:
		info.size = 0
	default:
		return nil, fmt.Errorf("서버 응답 HTTP %d", resp.StatusCode)
	}
	if et := resp.Header.Get("ETag"); et != "" && !strings.HasPrefix(et, "W/") {
		info.etag = et
	}
	info.lastMod = resp.Header.Get("Last-Modified")
	info.name = fileNameFrom(resp)
	return info, nil
}

func parseContentRangeTotal(cr string) (int64, bool) {
	// bytes 0-0/12345
	i := strings.LastIndexByte(cr, '/')
	if i < 0 || !strings.HasPrefix(cr, "bytes ") {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64)
	return n, err == nil && n >= 0
}

func parseContentRangeStart(cr string) (int64, bool) {
	cr = strings.TrimPrefix(cr, "bytes ")
	i := strings.IndexByte(cr, '-')
	if i < 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(cr[:i], 10, 64)
	return n, err == nil
}

func fileNameFrom(resp *http.Response) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, p, err := mime.ParseMediaType(cd); err == nil {
			if n := SanitizeName(p["filename"]); n != "" {
				return n
			}
		}
	}
	base := path.Base(resp.Request.URL.Path)
	if s, err := url.PathUnescape(base); err == nil {
		base = s
	}
	if n := SanitizeName(base); n != "" && n != "/" && n != "." {
		return n
	}
	return "download"
}

// SanitizeName makes a file name safe on both Windows and macOS.
func SanitizeName(n string) string {
	n = strings.TrimSpace(n)
	n = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		if r < 32 {
			return -1
		}
		return r
	}, n)
	n = strings.Trim(n, ". ")
	if len(n) > 200 {
		n = n[:200]
	}
	return n
}
