#!/bin/sh
set -eu

project_root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
cd "$project_root"
script_path="$project_root/deploy/minikube/local.sh"
state_dir="$project_root/tmp/minikube"

kube() { kubectl --context=sre-payments "$@"; }

managed_pid() {
  pid_file=$1
  [ -f "$pid_file" ] || return 1
  pid=$(cat "$pid_file")
  case "$pid" in ''|*[!0-9]*) return 1 ;; esac
  process_command=$(ps -p "$pid" -o command= 2>/dev/null) || return 1
  case "$process_command" in
    *"$script_path forward "*) printf '%s\n' "$pid" ;;
    *) return 1 ;;
  esac
}

stop_forwards() {
  for service in payments-api victorialogs grafana; do
    pid_file="$state_dir/$service.pid"
    if pid=$(managed_pid "$pid_file"); then
      kill "$pid" 2>/dev/null || true
    fi
    rm -f "$pid_file"
  done
}

forward() {
  namespace=$1
  service=$2
  ports=$3
  child_pid=
  trap 'if [ -n "$child_pid" ]; then kill "$child_pid" 2>/dev/null || true; wait "$child_pid" 2>/dev/null || true; fi; exit 0' INT TERM HUP
  while :; do
    kubectl --context=sre-payments -n "$namespace" port-forward --address=127.0.0.1 \
      --pod-running-timeout=60s "service/$service" "$ports" &
    child_pid=$!
    wait "$child_pid" || true
    child_pid=
    sleep 2 &
    child_pid=$!
    wait "$child_pid" || true
    child_pid=
  done
}

start_forward() {
  namespace=$1
  service=$2
  ports=$3
  pid_file="$state_dir/$service.pid"
  if managed_pid "$pid_file" >/dev/null; then
    return
  fi
  # Give the supervisor its own session, so it survives the invoking terminal.
  python3 - "$script_path" "$namespace" "$service" "$ports" \
    "$state_dir/$service.log" "$pid_file" <<'PY'
from pathlib import Path
import subprocess
import sys

with open(sys.argv[5], 'w') as log:
    process = subprocess.Popen(
        ['sh', sys.argv[1], 'forward', sys.argv[2], sys.argv[3], sys.argv[4]],
        stdin=subprocess.DEVNULL,
        stdout=log,
        stderr=subprocess.STDOUT,
        start_new_session=True,
    )
Path(sys.argv[6]).write_text(str(process.pid) + '\n')
PY
}

wait_forward() {
  service=$1
  local_port=$2
  path=$3
  attempt=0
  while [ "$attempt" -lt 30 ]; do
    if grep -q "Forwarding from 127.0.0.1:$local_port" "$state_dir/$service.log" \
      && curl --fail --silent --max-time 2 "http://127.0.0.1:$local_port$path" >/dev/null; then
      return
    fi
    attempt=$((attempt + 1))
    sleep 2
  done
  printf 'Не удалось открыть %s на порту %s. Проверьте, не занят ли порт.\n' "$service" "$local_port" >&2
  cat "$state_dir/$service.log" >&2
  return 1
}

show_urls() {
  cat <<'URLS'

Приложение: http://127.0.0.1:18080
VictoriaLogs: http://127.0.0.1:9428/select/vmui/
Grafana: http://127.0.0.1:13000/d/payments-logs
Grafana: admin / minikube-grafana-password

Логи локальных подключений: tmp/minikube/*.log
Остановить подключения: make minikube-ui-stop
URLS
}

up() {
  for dependency in minikube kubectl curl python3; do
    if ! command -v "$dependency" >/dev/null 2>&1; then
      printf 'Не найдена команда %s. Установите её перед запуском.\n' "$dependency" >&2
      exit 1
    fi
  done
  mkdir -p "$state_dir"

  if ! minikube -p sre-payments status >/dev/null 2>&1; then
    minikube start -p sre-payments --driver=docker --container-runtime=docker \
      --cpus=3 --memory=4096 --keep-context
  fi

  # Keep an existing scale setting; a new installation starts with one replica.
  api_replicas=$(kube -n sre-payments get deployment payments-api \
    --ignore-not-found -o jsonpath='{.spec.replicas}')
  worker_replicas=$(kube -n sre-payments get deployment payments-worker \
    --ignore-not-found -o jsonpath='{.spec.replicas}')
  api_replicas=${api_replicas:-1}
  worker_replicas=${worker_replicas:-1}

  printf '\nСборка образа приложения\n'
  minikube -p sre-payments image build --build-opt build-arg=VERSION=minikube \
    -t sre-payments:minikube .

  # A content-based tag changes the Pod template only when the image changes.
  image_id=$(minikube -p sre-payments ssh -- \
    "docker image inspect --format '{{.Id}}' sre-payments:minikube" | tr -d '\r')
  image_digest=${image_id#sha256:}
  case "$image_digest" in
    ''|*[!0-9a-f]*) printf 'Некорректный ID образа: %s\n' "$image_id" >&2; exit 1 ;;
  esac
  image_tag="minikube-$image_digest"
  minikube -p sre-payments ssh -- \
    "docker tag sre-payments:minikube sre-payments:$image_tag"

  kube apply -f deploy/minikube/namespace.yaml -f deploy/minikube/config.yaml \
    -f deploy/minikube/postgres.yaml
  kube -n sre-payments rollout status statefulset/postgres --timeout=300s

  # The migrator records applied migrations in PostgreSQL; run it with the current image.
  kube -n sre-payments delete job payments-migrate --ignore-not-found --wait=true --timeout=60s
  kube apply -f deploy/minikube/migrate.yaml
  if ! kube -n sre-payments wait --for=condition=complete job/payments-migrate --timeout=300s; then
    kube -n sre-payments logs job/payments-migrate --tail=50 || true
    exit 1
  fi

  # The repository overlay owns resources and config; this layer keeps the local scale and image.
  cat >"$state_dir/kustomization.yaml" <<EOF
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ../../deploy/observability
images:
  - name: sre-payments
    newTag: $image_tag
replicas:
  - name: payments-api
    count: $api_replicas
  - name: payments-worker
    count: $worker_replicas
EOF
  kube apply -k "$state_dir"
  for deployment in victorialogs tempo alloy grafana; do
    kube -n observability rollout status "deployment/$deployment" --timeout=600s
  done
  kube -n sre-payments rollout status deployment/payments-api --timeout=180s
  kube -n sre-payments rollout status deployment/payments-worker --timeout=180s
  ui
}

ui() {
  for dependency in kubectl curl python3; do
    if ! command -v "$dependency" >/dev/null 2>&1; then
      printf 'Не найдена команда %s. Установите её перед запуском.\n' "$dependency" >&2
      exit 1
    fi
  done
  mkdir -p "$state_dir"
  start_forward sre-payments payments-api 18080:8080
  start_forward observability victorialogs 9428:9428
  start_forward observability grafana 13000:3000
  if ! wait_forward payments-api 18080 /healthz \
    || ! wait_forward victorialogs 9428 /health \
    || ! wait_forward grafana 13000 /api/health; then
    stop_forwards
    exit 1
  fi
  printf '\nЛокальные подключения работают в фоне и восстанавливаются при замене подов.\n'
  show_urls
}

status() {
  kube -n sre-payments get pods,services,pvc --request-timeout=5s
  kube -n observability get pods,services,pvc --request-timeout=5s
  for service in payments-api victorialogs grafana; do
    if pid=$(managed_pid "$state_dir/$service.pid"); then
      printf 'Локальное подключение %s: PID %s\n' "$service" "$pid"
    else
      printf 'Локальное подключение %s: остановлено\n' "$service"
    fi
  done
  show_urls
}

case "${1:-up}" in
  up) up ;;
  status) status ;;
  ui) ui ;;
  ui-stop) stop_forwards ;;
  forward)
    [ "$#" -eq 4 ] || exit 2
    forward "$2" "$3" "$4"
    ;;
  *) printf 'Использование: sh deploy/minikube/local.sh up|status|ui|ui-stop\n' >&2; exit 2 ;;
esac
