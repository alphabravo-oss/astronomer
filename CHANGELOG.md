# Changelog

## Unreleased

### Changed

- **Cluster monitoring stack `enableGrafana` default.** When `enableGrafana` is omitted on a **new** (`not_configured`) cluster stack, Grafana is now **disabled** if the shared (fleet) Grafana family is healthy. Previously omitted `enableGrafana` always defaulted to `true` (kube-prometheus-stack chart default). Explicit `true` / `false` is unchanged. Already-configured stacks keep the historical `true` default on upgrade/replace so an omitted key does not strip an existing cluster Grafana.

  Cluster Grafana still talks to **this** cluster’s Prometheus (15d local retention) and survives an Astronomer outage. Fleet Grafana is the lobby (Thanos + logs) and is down when Astronomer is down. New cluster stacks no longer default Grafana on when the lobby exists.
