package http

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/brojonat/reddit-bounty-board/http/api"
	"github.com/golang-jwt/jwt"
)

func generateAccessToken(claims authJWTClaims) (string, error) {
	t := jwt.New(jwt.SigningMethodHS256)
	t.Claims = claims
	return t.SignedString([]byte(getSecretKey()))
}

func handleIssueSudoToken(l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email, ok := r.Context().Value(ctxKeyEmail).(string)
		if !ok {
			writeInternalError(l, w, fmt.Errorf("missing context key for basic auth email"))
			return
		}
		sc := jwt.StandardClaims{
			ExpiresAt: time.Now().Add(2 * 7 * 24 * time.Hour).Unix(), // 2 weeks
		}
		c := authJWTClaims{
			StandardClaims: sc,
			Email:          email,
			Status:         UserStatusSudo,
		}
		token, _ := generateAccessToken(c)
		resp := api.DefaultJSONResponse{Message: token}
		writeJSONResponse(w, resp, http.StatusOK)
	}
}
