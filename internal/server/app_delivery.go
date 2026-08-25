package server

import (
	"context"
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	deliverybuiltin "github.com/alphabravocompany/astronomer-go/internal/delivery/builtin"
	deliverydeployment "github.com/alphabravocompany/astronomer-go/internal/delivery/deployment"
	deliveryrollout "github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/systemrollout"
	deliveryhandler "github.com/alphabravocompany/astronomer-go/internal/handler/delivery"
)

func (c *productionComposition) initializeDelivery(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	database := c.database
	queries := c.queries
	encryptor := c.encryptor
	bus := c.bus
	clusterRegistrationHandler := c.clusterRegistrationHandler
	deliveryStatusIngester := c.deliveryStatusIngester
	rbacQuerier := c.rbacQuerier
	rbacEngine := c.rbacEngine
	requester := c.requester
	taskLeader := c.taskLeader
	deliveryPlanningStore, err := deliveryrollout.NewPostgresPlanningStore(database.Pool())
	if err != nil {
		database.Close()
		return err
	}
	deliveryPlanner, err := deliveryrollout.NewPlanner(deliveryPlanningStore, nil, nil)
	if err != nil {
		database.Close()
		return err
	}
	builtinProvisioner, err := deliverybuiltin.NewProvisioner(
		database.Pool(), deliveryPlanningStore, deliveryPlanner, clusterRegistrationHandler.Service(),
	)
	if err != nil {
		database.Close()
		return err
	}
	deliveryStatusIngester.SetReadyReconciler(builtinProvisioner)
	deliveryRolloutController, err := deliveryrollout.NewPostgresController(database.Pool(), nil)
	if err != nil {
		database.Close()
		return err
	}
	deliveryRolloutController.RequireTransactionalAudit()
	deliveryDeploymentController, err := deliverydeployment.NewPostgresController(database.Pool(), nil)
	if err != nil {
		database.Close()
		return err
	}
	deliveryDeploymentController.RequireTransactionalAudit()
	deliverySystemRolloutService, err := systemrollout.New(database.Pool())
	if err != nil {
		database.Close()
		return err
	}
	deliverySystemRolloutService.RequireTransactionalAudit()
	deliveryTargetHandler := deliveryhandler.NewTargetHandler(queries, deliveryPlanningStore, bus)
	deliveryTargetHandler.SetPlatformScopeChecker(queries)
	deliveryTargetHandler.SetRunTx(sqlcMutationTxRunner[deliveryhandler.TargetMutationTx](database))
	deliverySourceHandler := deliveryhandler.NewSourceHandler(queries, encryptor, 1)
	deliverySourceHandler.SetRunTx(sqlcMutationTxRunner[deliveryhandler.SourceMutationTx](database))
	deliveryBundleHandler := deliveryhandler.NewBundleHandler(queries)
	deliveryBundleHandler.SetRunTx(sqlcMutationTxRunner[deliveryhandler.BundleMutationTx](database))
	deliveryRolloutHandler := deliveryhandler.NewRolloutHandler(queries, deliveryPlanner, deliveryRolloutController, bus)
	deliveryRolloutHandler.EnableTransactionalPlannerAudit()
	deliverySystemRolloutHandler := deliveryhandler.NewSystemRolloutHandler(deliverySystemRolloutService, queries, bus)
	deliverySystemRolloutHandler.EnableTransactionalAudit()
	kubectlShell, kubectlSessionReapRuntime := kubectlShellComponents(queries, rbacQuerier, rbacEngine, requester, cfg, logger, taskLeader)
	c.deliveryPlanningStore = deliveryPlanningStore
	c.deliveryRolloutController = deliveryRolloutController
	c.deliveryDeploymentController = deliveryDeploymentController
	c.deliveryTargetHandler = deliveryTargetHandler
	c.deliverySourceHandler = deliverySourceHandler
	c.deliveryBundleHandler = deliveryBundleHandler
	c.deliveryRolloutHandler = deliveryRolloutHandler
	c.deliverySystemRolloutHandler = deliverySystemRolloutHandler
	c.kubectlShell = kubectlShell
	c.kubectlSessionReapRuntime = kubectlSessionReapRuntime
	return nil
}
