package policy

import "testing"

func TestCurrentPolicyIsAccepted(t *testing.T) {
	current := Current()
	if err := ValidateCreationRequest(current.Profile, current.Algorithm); err != nil {
		t.Fatalf("current policy rejected: %v", err)
	}
}

func TestUnsupportedProfilesFailClosed(t *testing.T) {
	profiles := []Profile{
		"PAdES_BASELINE_B_T",
		"PAdES_BASELINE_LT",
		"PAdES_BASELINE_LTA",
		"CAdES_BASELINE_B",
		"",
	}
	for _, profile := range profiles {
		t.Run(string(profile), func(t *testing.T) {
			if err := ValidateCreationRequest(profile, AlgorithmRSAPKCS1SHA256); err == nil {
				t.Fatalf("profile %q was accepted", profile)
			}
		})
	}
}

func TestUnsupportedAlgorithmsFailClosed(t *testing.T) {
	algorithms := []Algorithm{
		"RSA_PKCS1_SHA1",
		"RSA_PSS_SHA256",
		"ECDSA_SHA256",
		"DSA_SHA256",
		"MD5_RSA",
		"",
	}
	for _, algorithm := range algorithms {
		t.Run(string(algorithm), func(t *testing.T) {
			if err := ValidateCreationRequest(ProfilePAdESBaselineBB, algorithm); err == nil {
				t.Fatalf("algorithm %q was accepted", algorithm)
			}
		})
	}
}
