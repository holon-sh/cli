package clientutil

import (
	"context"
	"fmt"
	"net/http"
)

// Bearer returns a copy of request that carries token only if it goes to origin (scheme://host), redirects included.
func Bearer(request *http.Request, origin, token string) *http.Request {
	request = request.Clone(request.Context())
	if token != "" && request.URL.Scheme+"://"+request.URL.Host == origin {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return request
}

// Get reads at most limit bytes of the 200 response to a GET of location.
func Get(ctx context.Context, client *http.Client, location string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", location, response.StatusCode)
	}
	data, err := ReadLimited(response.Body, limit)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", location, err)
	}
	return data, nil
}
