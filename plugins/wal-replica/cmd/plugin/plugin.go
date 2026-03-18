// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package plugin

import (
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/http"
	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
	"github.com/cloudnative-pg/cnpg-i/pkg/reconciler"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/documentdb/cnpg-i-wal-replica/internal/identity"
	operatorImpl "github.com/documentdb/cnpg-i-wal-replica/internal/operator"
	reconcilerImpl "github.com/documentdb/cnpg-i-wal-replica/internal/reconciler"
)

// NewCmd creates the `plugin` command
func NewCmd() *cobra.Command {
	cmd := http.CreateMainCmd(identity.Implementation{}, func(server *grpc.Server) error {
		// Register the declared implementations
		operator.RegisterOperatorServer(server, operatorImpl.Implementation{})
		reconciler.RegisterReconcilerHooksServer(server, reconcilerImpl.Implementation{})
		return nil
	})

	logger := zap.New(zap.UseDevMode(true))
	log.SetLogger(logger)

	cmd.Use = "receivewal"

	return cmd
}
