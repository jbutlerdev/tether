//go:build production && forge

package main

import "github.com/jbutlerdev/tether/go/internal/forge"

func newForgeImpl(apiURL string) forge.Client {
	return forge.NewHTTPClient(apiURL)
}
