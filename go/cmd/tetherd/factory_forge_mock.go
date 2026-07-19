//go:build production && !forge

package main

import "github.com/jbutlerdev/tether/go/internal/forge"

func newForgeImpl(_ string) forge.Client {
	return forge.NewMockClient()
}
