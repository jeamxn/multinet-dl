package engine

import (
	"encoding/json"
	"os"
)

// control is the resume file written next to the .part file.
type control struct {
	Version      int              `json:"version"`
	URL          string           `json:"url"`
	FinalURL     string           `json:"finalUrl"`
	Size         int64            `json:"size"`
	ETag         string           `json:"etag,omitempty"`
	LastModified string           `json:"lastModified,omitempty"`
	Remaining    [][2]int64       `json:"remaining"`
	NetBytes     map[string]int64 `json:"netBytes"`
}

func (c *control) matches(u string, p *probeInfo) bool {
	if c.Version != 1 || c.URL != u || c.Size != p.size || p.size <= 0 {
		return false
	}
	if c.ETag != "" || p.etag != "" {
		return c.ETag == p.etag
	}
	if c.LastModified != "" || p.lastMod != "" {
		return c.LastModified == p.lastMod
	}
	return true
}

func loadControl(path string) (*control, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c control
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (j *Job) saveControl() {
	j.mu.Lock()
	if !j.rangeOK || j.fileName == "" || j.state == StateCanceled {
		j.mu.Unlock()
		return
	}
	c := control{Version: 1, URL: j.opts.URL, FinalURL: j.finalURL, Size: j.size, ETag: j.etag, LastModified: j.lastMod, NetBytes: map[string]int64{}}
	for _, s := range j.segs {
		if s.pos < s.end {
			c.Remaining = append(c.Remaining, [2]int64{s.pos, s.end})
		}
	}
	for _, n := range j.nets {
		c.NetBytes[n.id] = n.bytes.Load()
	}
	path := j.ctlPath()
	j.mu.Unlock()
	b, _ := json.Marshal(c)
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, path)
	}
}
