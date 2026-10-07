package main

import (
	"encoding/json"
	"errors"
	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
	"time"
)

func validateEstateDeliveryBody(raw []byte, m estateMember, mode string, a *estateAssignment) error {
	var envelope struct {
		Data       json.RawMessage `json:"data"`
		Pagination *struct {
			Total   *int `json:"total"`
			HasMore bool `json:"has_more"`
		} `json:"pagination"`
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Data) == 0 {
		return errors.New("invalid_delivery_body")
	}
	var deployments []estateDeployment
	switch mode {
	case "detail":
		var detail struct {
			Deployment estateDeployment `json:"deployment"`
		}
		if json.Unmarshal(envelope.Data, &detail) != nil {
			return errors.New("invalid_delivery_detail")
		}
		if detail.Deployment.ID != a.ID {
			return errors.New("foreign_deployment")
		}
		deployments = []estateDeployment{detail.Deployment}
	case "list":
		if json.Unmarshal(envelope.Data, &deployments) != nil || envelope.Pagination == nil || envelope.Pagination.Total == nil || *envelope.Pagination.Total != len(m.Assignments) || envelope.Pagination.HasMore {
			return errors.New("incomplete_delivery_list")
		}
	case "inventory":
		var inventory struct {
			Deployments []estateDeployment                       `json:"deployments"`
			Count       *int                                     `json:"deployment_count"`
			Controller  *astroclient.DeliveryControllerInventory `json:"controller_inventory"`
		}
		if json.Unmarshal(envelope.Data, &inventory) != nil || inventory.Count == nil || *inventory.Count != len(m.Assignments) || inventory.Controller == nil {
			return errors.New("incomplete_delivery_inventory")
		}
		controller := inventory.Controller
		if controller.ClusterId.String() != m.ClusterID || !controller.Ready || controller.ErrorCode != "" || !estateRecent(controller.ObservedAt, time.Now(), 5*time.Minute) {
			return errors.New("noncurrent_controller_inventory")
		}
		deployments = inventory.Deployments
	}
	expected := map[string]estateAssignment{}
	for _, item := range m.Assignments {
		expected[item.ID] = item
	}
	if a != nil {
		expected = map[string]estateAssignment{a.ID: *a}
	}
	if len(deployments) != len(expected) {
		return errors.New("incomplete_deployment_set")
	}
	now := time.Now()
	for _, d := range deployments {
		wanted, ok := expected[d.ID]
		if !ok || d.ClusterID != m.ClusterID || d.TargetID != wanted.TargetID || d.Desired != wanted.Generation || d.Observed != wanted.Generation || d.DesiredDigest != wanted.SpecDigest || d.ObservedDigest != wanted.SpecDigest || d.Action != "apply" || d.Phase != "ready" {
			return errors.New("deployment_identity_or_status")
		}
		if !estateRecent(d.LastObserved, now, 5*time.Minute) {
			return errors.New("stale_deployment")
		}
		// Modern metadata must corroborate source freshness; legacy receipt-age
		// checks are explicitly not a source-freshness proof.
		if observation := d.Inventory.Observation; observation != nil {
			if observation.State != "current" || !estateRecent(observation.ObservedAt, now, 5*time.Minute) {
				return errors.New("noncurrent_deployment")
			}
		}
		delete(expected, d.ID)
	}
	return nil
}
