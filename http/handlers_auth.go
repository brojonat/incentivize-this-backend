package http

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/brojonat/affiliate-bounty-board/http/api"
	"github.com/golang-jwt/jwt"
)

type UserStatus int

const (
	UserStatusRestricted = -4
	UserStatusDefault    = 0
	UserStatusPremium    = 4
	UserStatusSudo       = 8
)

func createSudoToken(email string) (string, error) {
	claims := authJWTClaims{
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: time.Now().Add(2 * 7 * 24 * time.Hour).Unix(), // 2 weeks
		},
		Email:  email,
		Status: UserStatusSudo,
		Tier:   int(abb.BountyTierSudo),
	}
	return generateAccessToken(claims)
}

func createUserToken(email string, expiresAt time.Time) (string, error) {
	claims := authJWTClaims{
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: expiresAt.Unix(),
		},
		Email:  email,
		Status: UserStatusPremium,
		Tier:   int(abb.BountyTierPremium),
	}
	return generateAccessToken(claims)
}

func createBlackHatToken(email string, expiresAt time.Time) (string, error) {
	claims := authJWTClaims{
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: expiresAt.Unix(),
		},
		Email:  email,
		Status: UserStatusPremium,
		Tier:   int(abb.BountyTierBlackHat),
	}
	return generateAccessToken(claims)
}

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
		token, err := createSudoToken(email)
		if err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to generate token: %w", err))
			return
		}
		resp := api.TokenResponse{
			AccessToken: token,
			TokenType:   "Bearer",
		}
		writeJSONResponse(w, resp, http.StatusOK)
	}
}

func handleIssueUserToken(l *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email, ok := r.Context().Value(ctxKeyEmail).(string)
		if !ok {
			writeInternalError(l, w, fmt.Errorf("missing context key for basic auth email"))
			return
		}
		// Use a 2-week expiration for consistency with sudo token
		expiresAt := time.Now().Add(2 * 7 * 24 * time.Hour)
		token, err := createUserToken(email, expiresAt)
		if err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to generate user token: %w", err))
			return
		}
		resp := api.TokenResponse{
			AccessToken: token,
			TokenType:   "Bearer",
		}
		writeJSONResponse(w, resp, http.StatusOK)
	}
}
