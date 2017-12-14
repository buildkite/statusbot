package statuspage

import (
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const DefaultAPIURL = "https://api.statuspage.io/v1/pages/"

type Client struct {
	apiKey     string
	pageID     string
	httpClient *http.Client
	url        *url.URL
}

func NewClient(apiKey, pageID string) (*Client, error) {
	u, err := url.Parse(DefaultAPIURL + pageID + "/")
	if err != nil {
		return nil, fmt.Errorf("url error parsing (%s): %s", pageID, err)
	}
	c := Client{
		apiKey:     apiKey,
		pageID:     pageID,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		url:        u,
	}
	return &c, nil
}
