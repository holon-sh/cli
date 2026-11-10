package clientutil

import "net/http"

// Bearer returns a copy of request that carries token only if it goes to origin (scheme://host), redirects included.
func Bearer(request *http.Request, origin, token string) *http.Request {
	request = request.Clone(request.Context())
	if token != "" && request.URL.Scheme+"://"+request.URL.Host == origin {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return request
}
