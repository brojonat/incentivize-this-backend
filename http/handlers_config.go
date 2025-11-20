package http

import (
	"net/http"

	"github.com/brojonat/affiliate-bounty-board/http/api"
)

func handleGetConfig(usdcMintAddress, escrowWallet string, platformFeePercent float64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSONResponse(w, api.ConfigResponse{
			USDCMintAddress:    usdcMintAddress,
			EscrowWallet:       escrowWallet,
			PlatformFeePercent: platformFeePercent,
		}, http.StatusOK)
	}
}
