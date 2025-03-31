package stools

import (
	"net/http"
)

// AdaptHandler wraps an http.HandlerFunc with a series of middlewares.
// Each middleware is applied in the order they are provided.
// This is used to chain together middleware functions for HTTP handlers.
func AdaptHandler(h http.HandlerFunc, middlewares ...func(http.HandlerFunc) http.HandlerFunc) http.HandlerFunc {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}
