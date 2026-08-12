package telemetry

import (
	"context"
	"testing"
)

func TestSetupUsesCompatibleResourceSchema(t *testing.T) {
	_, providers, err := Setup(t.Context(), "127.0.0.1:4317")
	if err != nil {
		t.Fatal(err)
	}
	shutdownContext, cancel := context.WithCancel(context.Background())
	cancel()
	_ = providers.Shutdown(shutdownContext)
}
