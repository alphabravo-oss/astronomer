#!/usr/bin/env bash

flux_readiness_timeout_seconds() {
    local duration="$1"
    case "$duration" in
        *s) printf '%d\n' "${duration%s}" ;;
        *m) printf '%d\n' "$(( ${duration%m} * 60 ))" ;;
        *) return 1 ;;
    esac
}

wait_flux_controller_deployment() {
    local namespace="$1" controller="$2" timeout="$3"
    if [[ "$controller" != source-controller ]]; then
        kubectl -n "$namespace" rollout status "deployment/$controller" --timeout="$timeout"
        return
    fi

    local timeout_seconds deadline state generation observed desired updated current available running
    timeout_seconds="$(flux_readiness_timeout_seconds "$timeout")"
    deadline=$((SECONDS + timeout_seconds))
    while ((SECONDS < deadline)); do
        state="$(kubectl -n "$namespace" get deployment/source-controller \
            -o go-template='{{.metadata.generation}} {{if .status.observedGeneration}}{{.status.observedGeneration}}{{else}}0{{end}} {{if .spec.replicas}}{{.spec.replicas}}{{else}}1{{end}} {{if .status.updatedReplicas}}{{.status.updatedReplicas}}{{else}}0{{end}} {{if .status.replicas}}{{.status.replicas}}{{else}}0{{end}} {{if .status.availableReplicas}}{{.status.availableReplicas}}{{else}}0{{end}}' \
            2>/dev/null || true)"
        if read -r generation observed desired updated current available <<<"$state" &&
            [[ "$generation" =~ ^[0-9]+$ && "$observed" =~ ^[0-9]+$ && "$desired" =~ ^[0-9]+$ &&
                "$updated" =~ ^[0-9]+$ && "$current" =~ ^[0-9]+$ && "$available" =~ ^[0-9]+$ ]]; then
            running="$(kubectl -n "$namespace" get pods -l app=source-controller \
                -o go-template='{{range .items}}{{if eq .status.phase "Running"}}x{{end}}{{end}}' \
                2>/dev/null | wc -c)"
            if ((observed >= generation && desired > 0 && updated == desired && current == desired &&
                available >= 1 && running >= desired)); then
                return 0
            fi
        fi
        sleep 2
    done

    echo "Flux source-controller did not reach leader-plus-warm-standby readiness within $timeout" >&2
    kubectl -n "$namespace" get deployment/source-controller -o wide >&2 || true
    kubectl -n "$namespace" get pods -l app=source-controller -o wide >&2 || true
    return 1
}
