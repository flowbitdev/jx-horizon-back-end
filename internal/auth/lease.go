package auth

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type LeaseToken struct {
	UserID     uuid.UUID `json:"user_id"`
	ServerTime int64     `json:"server_time"`
	Expiry     int64     `json:"expiry"`
	Signature  string    `json:"signature"`
}

// GenerateLease signs a new lease for a user
func GenerateLease(userID uuid.UUID) (*LeaseToken, error) {
	// 1. Get Private Key from environment
	keyHex := os.Getenv("LEASE_PRIVATE_KEY")
	if keyHex == "" {
		log.Error().Msg("LEASE_PRIVATE_KEY is not set in environment")
		return nil, fmt.Errorf("LEASE_PRIVATE_KEY not set")
	}

	privKeyBytes, err := hex.DecodeString(keyHex)
	if err != nil || len(privKeyBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid LEASE_PRIVATE_KEY")
	}
	privKey := ed25519.PrivateKey(privKeyBytes)

	// 2. Prepare Payload
	serverTime := time.Now().UnixMilli()
	expiry := serverTime + (7 * 24 * 60 * 60 * 1000) // 7 days lease

	payload := fmt.Sprintf("%s:%d:%d", userID.String(), serverTime, expiry)
	sig := ed25519.Sign(privKey, []byte(payload))

	return &LeaseToken{
		UserID:     userID,
		ServerTime: serverTime,
		Expiry:     expiry,
		Signature:  hex.EncodeToString(sig),
	}, nil
}
