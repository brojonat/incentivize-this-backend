package abb

import "strings"

// BountyTier represents the access level for a bounty.
type BountyTier int

const (
	// BountyTierSudo is for all bounties
	BountyTierSudo BountyTier = -8
	// BountyTierBlackHat is for bounties that are highly sensitive.
	BountyTierBlackHat BountyTier = 0
	// BountyTierPremium is for bounties available to premium users.
	BountyTierPremium BountyTier = 4
	// BountyTierAltruist is for bounties that are publicly visible.
	BountyTierAltruist BountyTier = 8
)

// DefaultBountyTier is the default tier for new bounties if not specified.
const DefaultBountyTier = BountyTierAltruist

// String returns the string representation of a BountyTier.
func (bt BountyTier) String() string {
	switch bt {
	case BountyTierBlackHat:
		return "BlackHat"
	case BountyTierPremium:
		return "Premium"
	case BountyTierAltruist:
		return "Altruist"
	default:
		return "Unknown"
	}
}

// FromString converts a string to a BountyTier.
// It returns the tier and true if the string is a valid tier, otherwise DefaultBountyTier and false.
func FromString(s string) (BountyTier, bool) {
	switch strings.ToLower(s) {
	case "blackhat":
		return BountyTierBlackHat, true
	case "premium":
		return BountyTierPremium, true
	case "altruist":
		return BountyTierAltruist, true
	default:
		return DefaultBountyTier, false
	}
}
