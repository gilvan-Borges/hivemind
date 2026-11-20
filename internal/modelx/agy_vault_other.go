//go:build !windows

package modelx

import "fmt"

func ReadLiveAgyOAuthCredential() (raw []byte, email string, err error) {
	return nil, "", fmt.Errorf("vault OAuth do agy disponível apenas no Windows")
}

func WriteLiveAgyOAuthCredential(raw []byte) error {
	return fmt.Errorf("vault OAuth do agy disponível apenas no Windows")
}

func DeleteLiveAgyOAuthCredential() error {
	return nil
}

func AgyProfileVaultPath(profileID string) (string, error) {
	return "", fmt.Errorf("vault OAuth do agy disponível apenas no Windows")
}

func HasAgyVaultSession(profileID string) (bool, string) {
	return false, ""
}

func SyncCurrentLiveAgyAccountToProfiles() (*ProviderProfile, error) {
	return nil, nil
}

func ActivateAgyProfileSession(profileID string) error {
	return nil
}

func ConnectNewAgyGoogleAccount(workspace string) (ProfilesOverview, error) {
	return ListProfilesOverview(workspace), fmt.Errorf("login multi-conta automático disponível no Windows")
}

func EnsureLiveAgyCredentialRestored() {}

func SubmitAgyOAuthCode(code, workspace string) (ProfilesOverview, error) {
	return ListProfilesOverview(workspace), fmt.Errorf("login multi-conta automático disponível no Windows")
}

func CancelAgyOAuthLogin(workspace string) ProfilesOverview {
	return ListProfilesOverview(workspace)
}
