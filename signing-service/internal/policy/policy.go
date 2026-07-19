package policy

import "fmt"

type Profile string

type Algorithm string

const (
	ProfilePAdESBaselineBB Profile = "PAdES_BASELINE_B_B"

	AlgorithmRSAPKCS1SHA256 Algorithm = "RSA_PKCS1_SHA256"
)

const Version = "pades-b-b-rsa-sha256-v1"

type CreationPolicy struct {
	Version   string    `json:"version"`
	Profile   Profile   `json:"profile"`
	Algorithm Algorithm `json:"algorithm"`
}

func Current() CreationPolicy {
	return CreationPolicy{
		Version:   Version,
		Profile:   ProfilePAdESBaselineBB,
		Algorithm: AlgorithmRSAPKCS1SHA256,
	}
}

func ValidateCreationRequest(profile Profile, algorithm Algorithm) error {
	current := Current()
	if profile != current.Profile {
		return fmt.Errorf("unsupported signing profile %q: only %q is enabled", profile, current.Profile)
	}
	if algorithm != current.Algorithm {
		return fmt.Errorf("unsupported signing algorithm %q: only %q is enabled", algorithm, current.Algorithm)
	}
	return nil
}
