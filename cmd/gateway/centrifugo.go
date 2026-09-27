package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// publication is one message to one Centrifugo channel.
type publication struct {
	Channel string `json:"channel"`
	Data    any    `json:"data"`
}

// publisher sends publications (one call = one batch).
type publisher interface {
	Publish(ctx context.Context, pubs []publication) error
}

// centrifugo publishes through Centrifugo's server HTTP API (v5, /api/batch). The API key is
// sent as a header and never appears in errors or logs; nor does the URL.
type centrifugo struct {
	url string
	key string
	hc  *http.Client
}

func newCentrifugo(apiURL, key string) *centrifugo {
	return &centrifugo{url: apiURL + "/batch", key: key, hc: &http.Client{Timeout: 5 * time.Second}}
}

func (c *centrifugo) Publish(ctx context.Context, pubs []publication) error {
	if len(pubs) == 0 {
		return nil
	}
	type cmd struct {
		Publish publication `json:"publish"`
	}
	body := struct {
		Commands []cmd `json:"commands"`
	}{}
	for _, p := range pubs {
		body.Commands = append(body.Commands, cmd{Publish: p})
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("centrifugo: bad API URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.key)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("centrifugo: publish failed (%T)", err) // the error text carries the URL
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("centrifugo: publish: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Replies []struct {
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"replies"`
	}
	if err := json.Unmarshal(rb, &out); err != nil {
		return fmt.Errorf("centrifugo: publish: undecodable reply")
	}
	for i, r := range out.Replies {
		if r.Error != nil {
			return fmt.Errorf("centrifugo: publish %s: error %d %s", pubs[i].Channel, r.Error.Code, r.Error.Message)
		}
	}
	return nil
}
