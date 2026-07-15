#!/usr/bin/env bash

# Wait for one coherent GatewayClass status snapshot to prove that the
# controller accepts the class and supports the installed Gateway API bundle.
# Both conditions must have observed the current object generation; a stale
# True condition is not readiness.
wait_for_gatewayclass() {
  local class_name="${1:?GatewayClass name is required}"
  local attempts="${2:-30}"
  local delay_seconds="${3:-2}"
  local attempt=1
  local snapshot=""
  local generation=""
  local conditions=""
  local accepted_status="Missing"
  local accepted_observed="Missing"
  local supported_status="Missing"
  local supported_observed="Missing"
  local entry=""
  local value=""
  local -a gateway_conditions=()

  if ! [[ "${attempts}" =~ ^[1-9][0-9]*$ ]]; then
    printf 'GatewayClass readiness attempts must be a positive integer (got %q)\n' "${attempts}" >&2
    return 2
  fi
  if ! [[ "${delay_seconds}" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
    printf 'GatewayClass readiness delay must be a non-negative number (got %q)\n' "${delay_seconds}" >&2
    return 2
  fi

  while (( attempt <= attempts )); do
    snapshot=$(kubectl get gatewayclass "${class_name}" \
      -o 'jsonpath={.metadata.generation}{"\t"}{range .status.conditions[*]}{.type}{"="}{.status}{"/"}{.observedGeneration}{";"}{end}' \
      2>/dev/null || true)
    generation="${snapshot%%$'\t'*}"
    if [[ "${snapshot}" == *$'\t'* ]]; then
      conditions="${snapshot#*$'\t'}"
    else
      conditions=""
    fi
    accepted_status="Missing"
    accepted_observed="Missing"
    supported_status="Missing"
    supported_observed="Missing"

    IFS=';' read -r -a gateway_conditions <<<"${conditions}"
    for entry in "${gateway_conditions[@]}"; do
      case "${entry}" in
        Accepted=*)
          value="${entry#Accepted=}"
          accepted_status="${value%%/*}"
          accepted_observed="${value#*/}"
          ;;
        SupportedVersion=*)
          value="${entry#SupportedVersion=}"
          supported_status="${value%%/*}"
          supported_observed="${value#*/}"
          ;;
      esac
    done

    if [[ "${generation}" =~ ^[1-9][0-9]*$ ]] \
      && [[ "${accepted_status}" == "True" ]] \
      && [[ "${accepted_observed}" == "${generation}" ]] \
      && [[ "${supported_status}" == "True" ]] \
      && [[ "${supported_observed}" == "${generation}" ]]; then
      printf 'GatewayClass %s is ready (generation=%s, Accepted=True, SupportedVersion=True)\n' \
        "${class_name}" "${generation}"
      return 0
    fi

    if (( attempt < attempts )); then
      sleep "${delay_seconds}"
    fi
    attempt=$((attempt + 1))
  done

  printf 'GatewayClass %s did not become ready after %s attempts (generation=%s, Accepted=%s observedGeneration=%s, SupportedVersion=%s observedGeneration=%s)\n' \
    "${class_name}" "${attempts}" "${generation:-Missing}" \
    "${accepted_status}" "${accepted_observed}" \
    "${supported_status}" "${supported_observed}" >&2
  return 1
}
