package auth

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

const flowScope = "https://service.flow.microsoft.com/.default"

// AccessToken returns a delegated Flow Service token.
// It checks FLOW_ACCESS_TOKEN, then the Azure CLI resource token, then device code
// when FLOW_TENANT_ID and FLOW_CLIENT_ID are set.
func AccessToken(ctx context.Context) (string, error) {
	if tok := strings.TrimSpace(os.Getenv("FLOW_ACCESS_TOKEN")); tok != "" {
		return tok, nil
	}

	tok, azErr := azureCLIToken(ctx)
	if azErr == nil && strings.TrimSpace(tok) != "" {
		return strings.TrimSpace(tok), nil
	}

	tenant := strings.TrimSpace(os.Getenv("FLOW_TENANT_ID"))
	clientID := strings.TrimSpace(os.Getenv("FLOW_CLIENT_ID"))
	if tenant != "" && clientID != "" {
		return deviceCodeToken(ctx, tenant, clientID)
	}

	if azErr == nil {
		azErr = fmt.Errorf("empty token")
	}
	return "", fmt.Errorf("no Flow API token: set FLOW_ACCESS_TOKEN, sign in with az login, or set FLOW_TENANT_ID and FLOW_CLIENT_ID for device code (%v)", azErr)
}

func azureCLIToken(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("az"); err != nil {
		return "", fmt.Errorf("azure cli not found")
	}
	cmd := exec.CommandContext(ctx, "az", "account", "get-access-token",
		"--resource", "https://service.flow.microsoft.com/",
		"--query", "accessToken",
		"-o", "tsv")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("az account get-access-token: %s", msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func deviceCodeToken(ctx context.Context, tenantID, clientID string) (string, error) {
	cred, err := azidentity.NewDeviceCodeCredential(&azidentity.DeviceCodeCredentialOptions{
		TenantID: tenantID,
		ClientID: clientID,
		UserPrompt: func(_ context.Context, message azidentity.DeviceCodeMessage) error {
			fmt.Fprintln(os.Stderr, message.Message)
			return nil
		},
	})
	if err != nil {
		return "", err
	}
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{flowScope},
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(tok.Token) == "" {
		return "", fmt.Errorf("device code login returned an empty token")
	}
	return tok.Token, nil
}
