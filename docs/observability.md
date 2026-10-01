# Мониторинг, логи и трейсы

## Включение

Мониторинг опционален. Базовый `docker compose up --build -d` запускает
приложение с PostgreSQL без Prometheus, Grafana, Loki, Tempo и Alloy.

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy:4318 docker compose --profile monitoring up --build -d
```

Либо выполните `make up-monitoring`. Профиль включает службы, а переменная
окружения отдельно включает экспорт трасс. При пустом endpoint экспортёр
и очередь не создаются, сетевых попыток экспорта нет. `trace_id`, `span_id`,
`request_id` в логах и `/metrics` доступны в обоих режимах; без экспорта
трейсы не сохраняются в Tempo. Само приложение не зависит от готовности Alloy.

Для возврата к базовому режиму:

```sh
docker compose --profile monitoring stop prometheus grafana loki tempo alloy
OTEL_EXPORTER_OTLP_ENDPOINT= docker compose up -d
```

Профили не останавливают ранее запущенные контейнеры автоматически.
Если endpoint сохранён в `.env`, очистите его. Данные мониторинга остаются в volumes.

## Потоки данных (при включённом мониторинге)

- API и worker → `/metrics` → Prometheus → Grafana.
- zap → stdout → Docker logs → Alloy → Loki → Grafana.
- OpenTelemetry → OTLP/HTTP → Alloy → OTLP/gRPC → Tempo → Grafana.

Prometheus измеряет процессы Go: CPU, RSS, goroutines, GC, файловые дескрипторы.
Прикладные метрики: количество и длительность HTTP-запросов, результат проходов
worker, время последнего успешного прохода и количество просроченных счетов.
Это мониторинг процессов приложения, не всех процессов хоста и не PostgreSQL;
node_exporter, cAdvisor и postgres_exporter пока не подключены.

В Loki labels — только project/service/container. `trace_id`, `span_id` и
`request_id` остаются JSON-полями: высокая кардинальность не раздувает индекс.
HTTP-метрики используют шаблон маршрута, а не идентификатор счёта.

## Отладка одного запроса

1. Выполните действие в приложении.
2. В DevTools → Network найдите `X-Trace-ID` ответа.
3. Grafana → Explore → Loki:

```logql
{project="sre-payments", service="api"} | json | trace_id="ВАШ_TRACE_ID"
```

4. Раскройте строку лога и нажмите **Open trace** в поле TraceID.
5. Tempo покажет HTTP span, сервисные операции и SQL spans.
6. Из Tempo доступен переход обратно к логам выбранного трейса.

Для логов worker используйте `service="worker"`. Каждый проход получает
отдельный корневой span `worker.expire`. Связь с исходным запросом выставления
счёта через span link пока не реализована: это разные операции во времени.

Ошибки сервисов и SQL помечают span как error. HTTP 5xx помечает серверный
span как error; ожидаемые HTTP 4xx видны по атрибуту status. zap добавляет
идентификаторы из контекста, а сервисы логируют длительность операций.
SQL-текст и аргументы не экспортируются. Авторизация и тела запросов не логируются.

В учебном окружении включён sampling 100%, включая входящий traceparent
с sampled=0. Сбой экспорта трейсов не прерывает бизнес-операцию; очередь экспорта
ограничена, при длительной недоступности коллектора часть трейсов может потеряться.
При завершении процесса экспорт ожидается не более 5 секунд.

## Источники и хранение

Grafana автоматически получает три источника данных и дашборд Payments.
Prometheus хранит метрики 7 дней, Loki — логи 7 дней, Tempo — трейсы 24 часа.
Все хранилища локальные в Docker volumes, без репликации.
Пароль Grafana задаётся при первом создании её базы; смена env не меняет
пароль уже существующего пользователя.

Alloy читает Docker API через socket и отбирает контейнеры проекта `sre-payments`.
Socket mount `:ro` не ограничивает операции Docker API; такой доступ доверяем
только локальному учебному коллектору. Для общего production-хоста нужен
ограничивающий proxy или другой способ доставки логов.

## Предупреждения

Prometheus содержит правила ProcessDown, WorkerStalled и HTTPServerErrors.
Их состояния видны на `/alerts`; отправка уведомлений не настроена, Alertmanager
не подключён. Отдельный heartbeat worker позволяет отличить живой HTTP endpoint
метрик от реально работающей обработки.

Для другого интервала worker согласуйте порог WorkerStalled в alerts.yaml.

## Документация используемых компонентов

- https://opentelemetry.io/docs/languages/go/
- https://grafana.com/docs/alloy/latest/reference/components/loki/loki.source.docker/
- https://grafana.com/docs/tempo/latest/
- https://pkg.go.dev/go.uber.org/zap

## История проверки полного окружения

Локально проверены: шесть scrape targets Prometheus со статусом `up`,
создание всех трёх Grafana datasources и загрузка дашборда, поиск одного запроса
в Loki и получение его из Tempo. Трейс выставления счёта содержит HTTP span,
`service.Change`, а также `postgres.select/begin/update/insert/commit`.

Локальный runtime-образ для этой проверки собран из Linux-бинарника,
скомпилированного нативным Go на macOS: существующая x86 Colima сильно
замедляет компиляцию. Полная multi-stage сборка Dockerfile настроена в CI;
локально эта полная сборка пока не проверена. GitHub Actions ещё не запускался,
поскольку изменения не закоммичены и не отправлены.
