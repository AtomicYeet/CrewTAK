// Package weather serves the latest METAR for the station's airport (KATW,
// Appleton International) to the wall display.
//
// The browser can't reliably reach the Aviation Weather Center directly, and
// the National Weather Service copy often runs an hour behind, so the server
// asks both, keeps whichever report is newer, and caches the answer briefly
// so every open display shares one lookup.
package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	auth "donetick.com/core/internal/auth"
	"github.com/gin-gonic/gin"
)

const (
	station   = "KATW"
	cacheFor  = 2 * time.Minute
	userAgent = "CrewTAK station display (self-hosted)"
)

// Metar is what the display shows on its METAR line.
type Metar struct {
	Station string    `json:"station"`
	Raw     string    `json:"raw"`
	TempC   *float64  `json:"tempC"`
	Text    string    `json:"text"`
	Time    time.Time `json:"time"`
	Source  string    `json:"source"`
}

type Handler struct {
	client *http.Client

	mu      sync.Mutex
	cached  *Metar
	fetched time.Time
}

func NewHandler() *Handler {
	return &Handler{client: &http.Client{Timeout: 10 * time.Second}}
}

func (h *Handler) getJSON(ctx context.Context, url, accept string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", accept)
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Aviation Weather Center: usually has the new METAR within minutes.
func (h *Handler) fromAWC(ctx context.Context) (*Metar, error) {
	var rows []struct {
		RawOb   string   `json:"rawOb"`
		Temp    *float64 `json:"temp"`
		ObsTime int64    `json:"obsTime"`
	}
	url := "https://aviationweather.gov/api/data/metar?ids=" + station + "&format=json"
	if err := h.getJSON(ctx, url, "application/json", &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 || strings.TrimSpace(rows[0].RawOb) == "" {
		return nil, errors.New("AWC returned no METAR")
	}
	r := rows[0]
	return &Metar{
		Station: station,
		Raw:     cleanRaw(r.RawOb),
		TempC:   r.Temp,
		Time:    time.Unix(r.ObsTime, 0).UTC(),
		Source:  "aviationweather.gov",
	}, nil
}

// National Weather Service: slower, but adds a plain-English sky description.
func (h *Handler) fromNWS(ctx context.Context) (*Metar, error) {
	var body struct {
		Properties struct {
			RawMessage      string    `json:"rawMessage"`
			TextDescription string    `json:"textDescription"`
			Timestamp       time.Time `json:"timestamp"`
			Temperature     struct {
				Value *float64 `json:"value"`
			} `json:"temperature"`
		} `json:"properties"`
	}
	url := "https://api.weather.gov/stations/" + station + "/observations/latest"
	if err := h.getJSON(ctx, url, "application/geo+json", &body); err != nil {
		return nil, err
	}
	p := body.Properties
	return &Metar{
		Station: station,
		Raw:     cleanRaw(p.RawMessage),
		TempC:   p.Temperature.Value,
		Text:    p.TextDescription,
		Time:    p.Timestamp.UTC(),
		Source:  "weather.gov",
	}, nil
}

// cleanRaw drops the leading "METAR " report-type word (the display already
// labels the line) but keeps "SPECI", which flags an unscheduled report.
func cleanRaw(raw string) string {
	return strings.TrimPrefix(strings.TrimSpace(raw), "METAR ")
}

func (h *Handler) latest(ctx context.Context) (*Metar, error) {
	type result struct {
		m   *Metar
		err error
	}
	awcCh, nwsCh := make(chan result, 1), make(chan result, 1)
	go func() { m, err := h.fromAWC(ctx); awcCh <- result{m, err} }()
	go func() { m, err := h.fromNWS(ctx); nwsCh <- result{m, err} }()
	awc, nws := <-awcCh, <-nwsCh

	var best *Metar
	for _, r := range []result{awc, nws} {
		if r.err != nil || r.m == nil || r.m.Raw == "" {
			continue
		}
		if best == nil || r.m.Time.After(best.Time) {
			best = r.m
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no METAR available (AWC: %v; NWS: %v)", awc.err, nws.err)
	}
	// Borrow NWS's sky description when the AWC report wins.
	if best.Text == "" && nws.err == nil && nws.m != nil {
		best.Text = nws.m.Text
	}
	return best, nil
}

func (h *Handler) metar(c *gin.Context) {
	if _, ok := auth.CurrentUser(c); !ok {
		c.JSON(401, gin.H{"error": "User not authenticated"})
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cached != nil && time.Since(h.fetched) < cacheFor {
		c.JSON(200, gin.H{"res": h.cached})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	m, err := h.latest(ctx)
	if err != nil {
		// Serve the last good report rather than nothing.
		if h.cached != nil {
			c.JSON(200, gin.H{"res": h.cached, "stale": true})
			return
		}
		c.JSON(502, gin.H{"error": err.Error()})
		return
	}
	h.cached, h.fetched = m, time.Now()
	c.JSON(200, gin.H{"res": m})
}

func Routes(r *gin.Engine, h *Handler, multiAuthMiddleware *auth.MultiAuthMiddleware) {
	g := r.Group("api/v1/crewtak/weather")
	g.Use(multiAuthMiddleware.MiddlewareFunc())
	{
		g.GET("metar", h.metar)
	}
}
