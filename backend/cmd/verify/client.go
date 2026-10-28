package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

// client talks to the API as one user.
type client struct {
	base  string
	http  *http.Client
	token string
	me    map[string]any
}

func newClient(base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{base: strings.TrimRight(base, "/"), http: &http.Client{Jar: jar, Timeout: 90 * time.Second}}
}

// response is a decoded API answer.
type response struct {
	status int
	body   map[string]any
	raw    string
	header http.Header
}

func (r response) str(key string) string {
	v, _ := r.body[key].(string)
	return v
}

func (r response) num(key string) float64 {
	v, _ := r.body[key].(float64)
	return v
}

func (r response) code() string {
	e, _ := r.body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func (r response) items() []map[string]any {
	list, _ := r.body["items"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, it := range list {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func (c *client) do(method, path string, body any, headers ...string) (response, error) {
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rd = strings.NewReader(b)
		default:
			buf, err := json.Marshal(b)
			if err != nil {
				return response{}, err
			}
			rd = bytes.NewReader(buf)
		}
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return response{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := response{status: resp.StatusCode, raw: string(raw), header: resp.Header}
	_ = json.Unmarshal(raw, &out.body)
	return out, nil
}

// must performs a request and requires the given status.
func (c *client) must(want int, method, path string, body any, headers ...string) (response, error) {
	r, err := c.do(method, path, body, headers...)
	if err != nil {
		return r, err
	}
	if r.status != want {
		snippet := r.raw
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		return r, fmt.Errorf("%s %s: HTTP %d, beklenen %d: %s", method, path, r.status, want, snippet)
	}
	return r, nil
}

func (c *client) login(email, password string) error {
	r, err := c.must(200, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
	if err != nil {
		return err
	}
	c.token = r.str("access_token")
	c.me, _ = r.body["user"].(map[string]any)
	return nil
}

func (c *client) employeeID() int64 {
	v, _ := c.me["employee_id"].(float64)
	return int64(v)
}
