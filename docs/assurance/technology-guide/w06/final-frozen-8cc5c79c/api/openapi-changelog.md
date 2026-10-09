# API Changelog 1.0 vs. 1.0


## API Changes

### GET /api/v1/delivery/rollouts/
- :warning: added the new `aborted` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `awaiting_approval` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `draft` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `failed` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `paused` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `progressing` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `queued` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `rejected` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `resolving` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `rollback_failed` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `rolled_back` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `rolling_back` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `succeeded` enum value to the `data/items/state` response property for the response status `200`


### GET /api/v1/delivery/rollouts/{id}/
- :warning: added the new `aborted` enum value to the `data/rollout/state` response property for the response status `200`
- :warning: added the new `awaiting_approval` enum value to the `data/rollout/state` response property for the response status `200`
- :warning: added the new `draft` enum value to the `data/rollout/state` response property for the response status `200`
- :warning: added the new `failed` enum value to the `data/rollout/state` response property for the response status `200`
- :warning: added the new `paused` enum value to the `data/rollout/state` response property for the response status `200`
- :warning: added the new `progressing` enum value to the `data/rollout/state` response property for the response status `200`
- :warning: added the new `queued` enum value to the `data/rollout/state` response property for the response status `200`
- :warning: added the new `rejected` enum value to the `data/rollout/state` response property for the response status `200`
- :warning: added the new `resolving` enum value to the `data/rollout/state` response property for the response status `200`
- :warning: added the new `rollback_failed` enum value to the `data/rollout/state` response property for the response status `200`
- :warning: added the new `rolled_back` enum value to the `data/rollout/state` response property for the response status `200`
- :warning: added the new `rolling_back` enum value to the `data/rollout/state` response property for the response status `200`
- :warning: added the new `succeeded` enum value to the `data/rollout/state` response property for the response status `200`


### GET /api/v1/delivery/rollouts/{id}/clusters/
- :warning: added the new `aborted` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `acknowledged` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `blocked` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `failed` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `pending` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `ready` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `ready_previous` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `reconciling` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `released` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `rollback_failed` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `rolling_back` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `skipped` enum value to the `data/items/state` response property for the response status `200`
- :warning: added the new `timed_out` enum value to the `data/items/state` response property for the response status `200`


### GET /api/v1/delivery/system/compatibility/
- :warning: added the new `aborted` enum value to the `data/current_rollout/allOf[#/components/schemas/DeliverySystemRollout]/state` response property for the response status `200`
- :warning: added the new `awaiting_approval` enum value to the `data/current_rollout/allOf[#/components/schemas/DeliverySystemRollout]/state` response property for the response status `200`
- :warning: added the new `draft` enum value to the `data/current_release/allOf[#/components/schemas/DeliverySystemRelease]/state` response property for the response status `200`
- :warning: added the new `draft` enum value to the `data/current_rollout/allOf[#/components/schemas/DeliverySystemRollout]/state` response property for the response status `200`
- :warning: added the new `failed` enum value to the `data/current_rollout/allOf[#/components/schemas/DeliverySystemRollout]/state` response property for the response status `200`
- :warning: added the new `paused` enum value to the `data/current_rollout/allOf[#/components/schemas/DeliverySystemRollout]/state` response property for the response status `200`
- :warning: added the new `progressing` enum value to the `data/current_rollout/allOf[#/components/schemas/DeliverySystemRollout]/state` response property for the response status `200`
- :warning: added the new `queued` enum value to the `data/current_rollout/allOf[#/components/schemas/DeliverySystemRollout]/state` response property for the response status `200`
- :warning: added the new `released` enum value to the `data/current_release/allOf[#/components/schemas/DeliverySystemRelease]/state` response property for the response status `200`
- :warning: added the new `retired` enum value to the `data/current_release/allOf[#/components/schemas/DeliverySystemRelease]/state` response property for the response status `200`
- :warning: added the new `revoked` enum value to the `data/current_release/allOf[#/components/schemas/DeliverySystemRelease]/state` response property for the response status `200`
- :warning: added the new `rollback_failed` enum value to the `data/current_rollout/allOf[#/components/schemas/DeliverySystemRollout]/state` response property for the response status `200`
- :warning: added the new `rolled_back` enum value to the `data/current_rollout/allOf[#/components/schemas/DeliverySystemRollout]/state` response property for the response status `200`
- :warning: added the new `rolling_back` enum value to the `data/current_rollout/allOf[#/components/schemas/DeliverySystemRollout]/state` response property for the response status `200`
- :warning: added the new `succeeded` enum value to the `data/current_rollout/allOf[#/components/schemas/DeliverySystemRollout]/state` response property for the response status `200`






