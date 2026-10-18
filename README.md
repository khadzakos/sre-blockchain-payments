# Платёжные счета

Учебный проект по SRE: Go, PostgreSQL, HTTP/REST и простой веб-интерфейс.
Архитектура — модульный монолит с упрощёнными портами и адаптерами.

## Что реализовано

- Создание, просмотр, изменение и удаление черновиков счетов.
- Выставление и отмена счёта, истечение срока отдельным worker.
- История изменений, атомарные записи и оптимистическая блокировка по версии.
- Точные суммы в минимальных единицах токена, без `float`.
- Учебный интерфейс без авторизации, с примерами для заполнения счетов.
- JSON-логи zap с корреляцией запросов; опционально Prometheus, Grafana, VictoriaLogs, Tempo и Alloy.

Подписание переводов, проверка блокчейна, попытки оплаты, статус `paid`
и escrow пока не реализованы. Они описаны в целевой модели и будут добавлены
следующими этапами. Интерфейс не имитирует оплату и не может вручную выставить `paid`.

## Запуск

Нужны Docker с работающим daemon и Docker Compose. Локальный Go не требуется.
На macOS с Colima сначала выполните `colima start --cpu 2 --memory 4`.
Для полного окружения и сборки желательно выделить 4–6 ГБ RAM.

```sh
cp .env.example .env
# Для локального учебного запуска достаточно значений из примера.
docker compose up --build -d
```

Если установлен отдельный Homebrew Compose, используйте `docker-compose`
вместо `docker compose` во всех командах.

- Приложение: http://localhost:8080 — открывается без входа.
  При обычном запуске работают только PostgreSQL, миграции, API и worker.
  Экспорт трасс отключён; JSON-логи доступны через `docker compose logs`.

### Запуск с мониторингом

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy:4318 docker compose --profile monitoring up --build -d
# Или: make up-monitoring
```

Профиль включает пять дополнительных служб. Переменная включает экспорт
трасс в Alloy; один только профиль включает сбор логов и метрик, без экспорта трасс.
Адрес можно сохранить в `.env`, чтобы не указывать его в командной строке.

- Grafana: http://localhost:3000 — пользователь `admin`, пароль из `GRAFANA_ADMIN_PASSWORD`.
- Prometheus: http://localhost:9090.
- VictoriaLogs vmui: http://localhost:9428/select/vmui/.
- Дашборд логов: **SRE Payments → Payments · Logs & traces**.
- Дашборд Grafana: **SRE Payments → Payments · Processes, requests & logs**.

Порты опубликованы только на localhost. PostgreSQL, VictoriaLogs, Tempo и Alloy
доступны по внутренней сети Compose. Данные сохраняются в named volumes.
Пример `.env.example` предназначен только для локальной разработки.
Если в пароле PostgreSQL есть специальные символы URL, их нужно корректно
кодировать в `DATABASE_URL`; для этого шаблона используйте буквенно-цифровой пароль.

```sh
docker compose ps
docker compose logs -f api worker
docker compose --profile monitoring stop
# Остановить и удалить контейнеры, сохранив данные:
docker compose --profile monitoring down
```

API, worker и миграции используют один образ. `migrate` — отдельная команда,
Compose ждёт её успешного завершения перед запуском приложения.
Для повторного применения миграций: `docker compose run --rm migrate`.

Чтобы вернуться к базовому запуску и остановить мониторинг без удаления данных:

```sh
docker compose --profile monitoring stop prometheus grafana victorialogs tempo alloy
OTEL_EXPORTER_OTLP_ENDPOINT= docker compose up -d
```

Если адрес экспорта сохранён в `.env`, очистите его для последующих базовых запусков.

## Как устроен код

```text
cmd/payments/         сборка зависимостей и команды api / worker / migrate
internal/domain/     счёт, условия и переходы; без внешних библиотек
internal/service/    сценарии и интерфейс Repository
internal/repository/ PostgreSQL-адаптер, миграции, SQL tracing
internal/handler/    REST API и HTTP instrumentation
internal/worker/     фоновые проходы истечения срока
internal/config/     конфигурация окружения
internal/telemetry/  zap, OpenTelemetry и Prometheus
web/                 встроенный в бинарник фронтенд
migrations/          SQL-миграции с проверкой контрольных сумм
observability/       конфигурация мониторинга и Grafana provisioning
```

HTTP-хендлер вызывает сервис. Сервис работает с доменной моделью и портом
`Repository`. PostgreSQL-адаптер реализует этот порт. Зависимости связываются
в `cmd/payments`, домен не знает о HTTP или SQL. Телеметрия — сквозная
техническая зависимость сервисов и адаптеров; доменная модель от неё свободна.

Мутация счёта и событие истории фиксируются одной транзакцией. Обновление
проверяет ожидаемую `version`, поэтому две реплики не перезапишут изменения
друг друга. При конфликте API возвращает `409`, клиент должен перечитать счёт.
Worker хранит всю необходимую информацию в базе и может быть перезапущен.

## Проверки

Автоматические unit-, HTTP- и интеграционные тесты отложены до следующего
этапа по решению владельца проекта. Тестовых файлов и сценариев запуска
тестовой базы в текущей версии нет.

Сохраняются golangci-lint, форматирование Go, Prettier, проверка конфигурации
Compose и сборка приложения. Команды приведены ниже.

## Дальнейшее развитие

1. Проверить Base Sepolia, faucet и RPC; окончательно выбрать сеть и токен.
2. Добавить подтверждение владения адресом, попытки оплаты и EVM-адаптер.
3. Реализовать зачёт, повторные проверки, поздние переводы и реорганизации.
4. Развивать нагрузочные тесты, балансировку и восстановление.
5. Добавить escrow после отработки надёжности базовых платежей.

Подробности: [модель](docs/domain.md), [API](docs/api.md),
[мониторинг и отладка](docs/observability.md), [отчёт](reports/Отчёт_HW1.md).

## Обязательные проверки качества

Используем `golangci-lint` и закреплённый Prettier.

```sh
npm ci --ignore-scripts
make format
make lint
```

Go форматируется с лимитом строки 110 символов, фронтенд — 100.
GitHub Actions выполняет lint, проверку форматирования, валидацию Compose
и сборку образа после push. Тесты и тестовая PostgreSQL из CI удалены. Настроенные правила работы находятся в `AGENTS.md`.

## Запуск в minikube

Манифесты находятся в `deploy/minikube/`. Команды выполнять из корня проекта.

Для запуска всего стенда одной командой нужен уже работающий Docker и установленные
`minikube`, `kubectl`, `curl`, `python3` и `make`:

```sh
make minikube-up
```

Команда запускает профиль `sre-payments`, собирает образ, разворачивает PostgreSQL,
выполняет миграции, запускает API, worker и инфраструктуру наблюдаемости.
Существующее количество реплик API и worker сохраняется. Управление Docker и Colima
в эту команду не входит.

После завершения доступны приложение <http://127.0.0.1:18080>,
vmui <http://127.0.0.1:9428/select/vmui/> и Grafana <http://127.0.0.1:13000/d/payments-logs>
(`admin` / `minikube-grafana-password`). Локальные `port-forward` запускаются в фоне
и восстанавливаются при замене подов. Их PID и логи находятся в `tmp/minikube/`.

```sh
make minikube-status
make minikube-ui
make minikube-ui-stop
```

`make minikube-ui` открывает только локальные подключения к уже работающему кластеру.
Последняя команда останавливает только созданные этим запуском локальные подключения;
поды продолжают работать. После перезагрузки Mac или остановки minikube снова выполните
`make minikube-up`. Для применения конфигурации к уже работающему приложению используется
`kubectl --context=sre-payments apply -k deploy/observability`. `make up` запускает Docker Compose.

Ниже приведены ручные шаги для разбора и выполнения задания по Kubernetes.

Запустить Docker и однодовый кластер:

```sh
colima start --cpu 4 --memory 6
minikube start -p sre-payments --driver=docker --container-runtime=docker --nodes=1 --cpus=3 --memory=4096 --keep-context
```

Собрать образ в Docker daemon minikube и явно загрузить его через архив:

```sh
eval "$(minikube -p sre-payments docker-env --shell bash)"
docker build --build-arg VERSION=minikube -t sre-payments:minikube .
image_archive=$(mktemp "${TMPDIR:-/tmp}/sre-payments-image.XXXXXX")
docker save -o "$image_archive" sre-payments:minikube
minikube -p sre-payments image load "$image_archive"
rm "$image_archive"
eval "$(minikube -p sre-payments docker-env --unset --shell bash)"
```

Образ уже доступен ноде после сборки; явная загрузка выполняет соответствующий
пункт задания. Передача архива исключает выбор внешнего daemon Colima.
Последняя команда возвращает Docker CLI к обычному окружению.

При ошибке DNS `lookup registry-1.docker.io ... server misbehaving` на этом Mac
можно проверить локальный resolver Colima и применить его только к учебной ноде:

```sh
docker --context colima exec sre-payments nslookup registry-1.docker.io 192.168.5.2
# Выполнить следующую команду только после успешного разрешения адреса выше.
docker --context colima exec sre-payments sh -c '
  if [ ! -f /etc/resolv.conf.payments-original ]; then
    cp /etc/resolv.conf /etc/resolv.conf.payments-original
  fi
  printf "nameserver 192.168.5.2\noptions ndots:0\n" > /etc/resolv.conf
'
```

Адрес `192.168.5.2` проверен в этом окружении; на другом компьютере он может
отличаться. После исправления DNS повторить сборку.

Применить конфигурацию и дождаться PostgreSQL:

```sh
kubectl --context=sre-payments apply -f deploy/minikube/namespace.yaml
kubectl --context=sre-payments apply -f deploy/minikube/config.yaml
kubectl --context=sre-payments apply -f deploy/minikube/postgres.yaml
kubectl --context=sre-payments -n sre-payments rollout status statefulset/postgres --timeout=180s
```

После успешной готовности базы применить миграции и дождаться их завершения:

```sh
kubectl --context=sre-payments apply -f deploy/minikube/migrate.yaml
kubectl --context=sre-payments -n sre-payments wait --for=condition=complete job/payments-migrate --timeout=300s
```

Только после успешного завершения миграций запустить API и worker:

```sh
kubectl --context=sre-payments apply -f deploy/minikube/api.yaml -f deploy/minikube/worker.yaml
kubectl --context=sre-payments -n sre-payments rollout status deployment/payments-api --timeout=180s
kubectl --context=sre-payments -n sre-payments rollout status deployment/payments-worker --timeout=180s
kubectl --context=sre-payments -n sre-payments get pods -o wide
```

Для демонстрации масштабирования увеличить API с одной до трёх реплик,
а worker — с одной до двух:

```sh
kubectl --context=sre-payments -n sre-payments scale deployment/payments-api --replicas=3
kubectl --context=sre-payments -n sre-payments scale deployment/payments-worker --replicas=2
kubectl --context=sre-payments -n sre-payments rollout status deployment/payments-api --timeout=180s
kubectl --context=sre-payments -n sre-payments rollout status deployment/payments-worker --timeout=180s
kubectl --context=sre-payments -n sre-payments get deployments,pods -o wide
```

Открыть локальный туннель и оставить команду работающей:

```sh
kubectl --context=sre-payments -n sre-payments port-forward --address=127.0.0.1 service/payments-api 18080:8080
```

Приложение доступно по адресу <http://127.0.0.1:18080>. Все команды Kubernetes
явно выбирают отдельный локальный контекст `sre-payments`.

## VictoriaLogs и трейсы в minikube

В том же кластере можно развернуть VictoriaLogs single-node, Alloy, Tempo и Grafana.
Приложение остаётся в `sre-payments`, инфраструктура — в `observability`.
Alloy читает JSON-логи API, worker и миграций через Kubernetes API и принимает
OTLP/HTTP spans. VictoriaLogs и Tempo хранят данные семь дней на отдельных PVC.

После запуска приложения по инструкции выше примените Kustomize overlay:

```sh
kubectl --context=sre-payments apply -k deploy/observability
```

`observability/kustomization.yaml` создаёт ConfigMap из общих файлов настроек.
`deploy/minikube/kustomization.yaml` описывает постоянно работающие компоненты приложения.
`deploy/observability/kustomization.yaml` объединяет приложение и инфраструктуру,
добавляет генерируемый ConfigMap `payments-telemetry` с адресом Alloy для API и worker.
Он подключается последним в `envFrom` и задаёт адрес экспорта трейсов поверх базовых настроек.
В overlay задано три реплики API и две worker; `make minikube-up` сохраняет текущий масштаб.

При изменении генерируемого ConfigMap меняется хэш в его имени и ссылка в шаблоне
пода. После `kubectl apply -k` Deployment обновляет поды автоматически. Принудительный
`rollout restart` не требуется. Неизменённые конфигурации повторный apply не перезапускает.
Предварительный просмотр: `kubectl kustomize deploy/observability`.

После изменений кода используйте `make minikube-up`: он собирает образ, присваивает
ему тег по содержимому и применяет Kustomize с новым тегом. Миграции выполняются
отдельным Job после готовности PostgreSQL; этот Job не входит в overlay наблюдаемости.
Все команды явно используют локальный контекст `sre-payments`.

В отдельных терминалах оставьте работающими туннели:

```sh
kubectl --context=sre-payments -n observability port-forward --address=127.0.0.1 service/victorialogs 9428:9428
kubectl --context=sre-payments -n observability port-forward --address=127.0.0.1 service/grafana 13000:3000
```

- vmui: <http://127.0.0.1:9428/select/vmui/>.
- Grafana: <http://127.0.0.1:13000>, `admin` / `minikube-grafana-password`
  (учебный локальный пароль из Secret; меняйте перед публикацией).
- Grafana → **SRE Payments → Payments · Logs & traces** или **Explore → VictoriaLogs**.
- Поля `trace_id`, `span_id`, `request_id` доступны для поиска; из `trace_id`
  можно открыть полный трейс в Tempo и вернуться к логам выбранного span.

Повторные попытки доставки включены. Очереди ограничены и не гарантируют
сохранение недоставленных данных при длительном сбое или перезапуске Alloy.
В Kubernetes используется встроенный сбор через API: монтировать каталоги ноды
или Docker socket для него не требуется. `loki.*` в конфигурации Alloy — названия
компонентов совместимого протокола доставки; сервер Loki заменён VictoriaLogs.

Подробности и запросы: [наблюдаемость](docs/observability.md), [отчёт ДЗ №3](reports/Отчёт_HW3.md).

## VictoriaMetrics и метрики PostgreSQL

Для задания по метрикам подготовлен отдельный Helm-релиз VictoriaMetrics
со встроенным сборщиком. API, worker и postgres-exporter обнаруживаются по
аннотациям подов. Три новых бизнес-счётчика отражают создание, выставление
и отмену счетов; существующий счётчик отражает истечение срока оплаты.

Команды выполнять из корня проекта. Helm устанавливается отдельно.

Подготовить манифесты для просмотра без изменения кластера:

```sh
mkdir -p tmp/metrics
helm template vm-single victoria-metrics-single \
  --repo https://victoriametrics.github.io/helm-charts/ \
  --version 0.49.0 \
  --namespace observability \
  -f deploy/observability/victoriametrics/values.yaml \
  > tmp/metrics/victoriametrics-rendered.yaml
```

Обновить приложение и установить VictoriaMetrics:

```sh
make minikube-up

kubectl --context=sre-payments apply \
  -f deploy/minikube/namespace.yaml \
  -f deploy/observability/namespace.yaml \
  -f deploy/observability/victoriametrics/access.yaml

helm upgrade --install vm-single victoria-metrics-single \
  --repo https://victoriametrics.github.io/helm-charts/ \
  --version 0.49.0 \
  --kube-context sre-payments \
  --namespace observability \
  -f deploy/observability/victoriametrics/values.yaml \
  --wait --timeout 5m
```

Конфигурация: `deploy/observability/victoriametrics/values.yaml`, права discovery: `deploy/observability/victoriametrics/access.yaml`.
[Отчёт по метрикам](reports/Отчёт_HW4.md).
