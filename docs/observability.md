# Метрики, логи и трейсы

## Состав окружений

Базовый Compose запускает PostgreSQL, миграции, API и worker. Профиль `monitoring`
добавляет Prometheus, VictoriaLogs, Tempo, Alloy и Grafana:

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy:4318 docker compose --profile monitoring up --build -d
```

Если Compose установлен отдельно, используйте `docker-compose`. Для возврата к
базовому запуску остановите `prometheus grafana victorialogs tempo alloy` и очистите
`OTEL_EXPORTER_OTLP_ENDPOINT`. Профиль сам по себе включает сбор логов и метрик;
переменная отдельно включает экспорт трейсов. При пустом endpoint идентификаторы
в логах генерируются, но полные spans не сохраняются.

В minikube приложение и PostgreSQL находятся в `sre-payments`; VictoriaLogs single-node,
Tempo, Alloy и Grafana — в `observability` того же кластера. Prometheus остаётся в Compose.
Для запуска всего стенда с уже работающим Docker выполните `make minikube-up`.
Команда запускает minikube, собирает образ, разворачивает приложение и инфраструктуру,
открывает локальный доступ к приложению, vmui и Grafana в фоне. Подключения
восстанавливаются при замене подов; отдельные терминалы для `port-forward` не нужны.
Команда не запускает Colima и не управляет Docker. `make minikube-status` показывает
состояние, `make minikube-ui` открывает подключения к уже работающему кластеру,
`make minikube-ui-stop` останавливает их. Для запуска фоновых подключений используется Python 3.
Для применения конфигурации к уже работающему приложению используется
`kubectl --context=sre-payments apply -k deploy/observability`.
После изменений кода повторите `make minikube-up`: новый образ получает тег по содержимому,
и изменение ссылки на образ запускает обновление подов. Подробности находятся в README.

Overlay объединяет приложение из `deploy/minikube/` с ресурсами инфраструктуры.
Генераторы в `observability/kustomization.yaml` читают общие файлы Alloy, Tempo и Grafana.
Отдельный генерируемый ConfigMap `payments-telemetry` в namespace `sre-payments`
задаёт адрес Alloy; он подключается последним в `envFrom` API и worker.
Генерируемые ConfigMap имеют хэш в имени. При изменении содержимого и повторном apply
ссылка в шаблоне пода меняется, и Deployment обновляет поды без принудительного restart.
Это стандартная [схема Kustomize](https://kubernetes.io/docs/tasks/manage-kubernetes-objects/kustomization/).

## Потоки данных

- JSON zap → stdout → Docker API или Kubernetes Pod logs API → Alloy → VictoriaLogs.
- OpenTelemetry → OTLP/HTTP Alloy `:4318` → OTLP/gRPC Tempo `:4317`.
- VictoriaLogs и Tempo → отдельные источники Grafana, связанные идентификатором трейса.
- `/metrics` API и worker → Prometheus в Compose → Grafana.

Alloy использует Loki-совместимый endpoint VictoriaLogs `/insert/loki/api/v1/push`.
Компоненты `loki.source.*` и `loki.write` описывают протокол доставки; отдельного
сервера Loki в актуальном окружении нет. VictoriaLogs разбирает JSON при поступлении,
поэтому поля доступны для фильтрации без `unpack_json` в каждом запросе.

В Kubernetes Alloy работает как один Deployment. Он обнаруживает только поды API,
worker и миграций в `sre-payments` и читает их логи через API. Role разрешает
`get/list/watch pods` и `get pods/log`, без доступа к Secrets. Docker socket и
каталоги ноды не монтируются. Для большого кластера вместо одного API-сборщика
можно использовать сбор файлов на нодах, но для этого стенда он не требуется.

## Схема JSON-логов

| Поле                                    | Содержание                                                |
| --------------------------------------- | --------------------------------------------------------- |
| `timestamp`                             | ISO8601 со смещением UTC                                  |
| `level`                                 | Уровень zap в нижнем регистре                             |
| `message`                               | Сообщение события                                         |
| `service.name`                          | `payments-api`, `payments-worker`, `payments-migrate`     |
| `service.version`                       | Версия сборки                                             |
| `trace_id`                              | Идентификатор всего трейса, если контекст span существует |
| `span_id`                               | Идентификатор текущей операции внутри трейса              |
| `request_id`                            | Идентификатор HTTP-запроса, если он есть в контексте      |
| `method`, `route`, `status`, `duration` | Итог HTTP-запроса; длительность в секундах                |
| `operation`, `error`                    | Операция и ошибка, если применимы                         |

Alloy дополнительно передаёт `project`, `environment` и поля источника. В minikube
это `namespace`, `pod`, `container`, `app`; в Compose — `service`, `container`.
Только эти поля определяют поток. `trace_id`, `span_id`, `request_id` остаются обычными
поисковыми полями. В VictoriaLogs время доступно как `_time`, сообщение — как `_msg`.
Стартовые записи процесса могут не содержать идентификаторов запроса и span.

## Поиск одного запроса и его spans

1. Выполните HTTP-запрос. Возьмите `X-Request-ID` и `X-Trace-Id` из заголовков ответа.
2. Откройте Grafana → Explore → VictoriaLogs, выберите подходящий диапазон времени.
3. Найдите запрос или весь трейс:

```logsql
request_id:="ВАШ_REQUEST_ID"
```

```logsql
trace_id:="ВАШ_TRACE_ID" | sort by (_time)
```

4. Раскройте запись и нажмите **Open trace** у поля `trace_id`.
5. Tempo покажет серверный HTTP span, сервисные операции и SQL spans.
6. Из выбранного span нажмите **Related logs**. Запрос фильтрует одновременно
   `trace_id` и `span_id`, с запасом времени ±1 минута.
7. Для всех логов запроса используйте **Request logs** у поля `request_id`.

Каждый дочерний span имеет собственный `span_id`, общий `trace_id` и, для HTTP,
общий `request_id`. Поэтому фильтр по одному `span_id` показывает только эту операцию,
а фильтр по `trace_id` — все её связанные операции. Grafana обновлена до версии,
поддерживающей VictoriaLogs в обратном переходе из Tempo.

Проходы worker создают отдельные корневые трейсы `worker.expire`, без HTTP request ID.
Связь с предыдущим запросом создания или выставления счёта не моделируется.
Поиск истории счёта через `invoice_id` в эту итерацию не включён.

HTTP 5xx и ошибки операций помечают соответствующие spans как ошибочные.
Ожидаемые 4xx видны по `status`; они не обязательно имеют `level:error`.
SQL-текст, аргументы SQL и тела запросов не экспортируются.
Sampling — 100% для учебного стенда, включая входящий `traceparent` с `sampled=0`.

## Четыре запроса для задания

Откройте VictoriaLogs vmui по `/select/vmui/` (канонический путь в установленной версии).
Выберите последние 15 минут и выполняйте запросы по очереди:

1. Сырые записи приложения за последние 15 минут:

```logsql
_time:15m project:="sre-payments" | sort by (_time) desc | limit 20
```

2. Фильтр по полю: HTTP-ответы API с кодом 400 и выше:

```logsql
_time:15m project:="sre-payments" service.name:="payments-api" _msg:="http request" status:>=400
```

3. Количество записей с группировкой по сервису и уровню:

```logsql
_time:15m project:="sre-payments" | stats by (service.name, level) count() as logs
```

4. Пять самых частых HTTP-маршрутов:

```logsql
_time:15m project:="sre-payments" service.name:="payments-api" route:* | top 5 by (route)
```

Дополнительно можно оценить число запросов и p95 длительности HTTP-обработки:

```logsql
_time:15m service.name:="payments-api" _msg:="http request" | stats count_uniq(request_id) as requests, quantile(0.95, duration) as p95_seconds
```

Агрегации `stats` и `top` удобно смотреть в табличном режиме vmui.
В Grafana преднастроен дашборд **Payments · Logs & traces**: логи, ошибки HTTP,
группировка и популярные маршруты. Официальный плагин VictoriaLogs устанавливается
до запуска Grafana, источники и связи создаются через provisioning.

## Хранение и поведение при сбоях

VictoriaLogs: семь дней (`-retentionPeriod=7d`); Tempo: 168 часов (`block_retention`).
В minikube используются отдельные PVC: VictoriaLogs 2Gi, Tempo 2Gi, Grafana 1Gi.
В Compose — named volumes. Принятые данные переживают замену подов/контейнеров.
Удаление самого minikube-кластера или томов может удалить данные. Репликации и
резервного копирования в учебном стенде нет. Запрошенный размер local-path PVC
не следует считать жёстким ограничением дискового потребления.

Alloy повторяет отправку логов до 10 раз; экспорт spans использует очередь в памяти
и повторные попытки. Экспортёр приложения также имеет ограниченную очередь.
Длительная недоступность, переполнение очереди или перезапуск сборщика могут
привести к потере недоставленных данных. Сбой телеметрии не прерывает обработку
платёжного запроса. Гарантия доставки без потерь и очередь на диске не заявляются. При перезапуске
API-сборщик может повторно прочитать доступные логи подов, поэтому `count()` считает
записи, включая повторы. Для числа HTTP-запросов используйте `count_uniq(request_id)`
с фильтром `_msg:="http request"`.

Пароль Grafana применяется при первоначальном создании базы. Изменение Secret/env
не меняет пароль уже существующего пользователя. Compose сохраняет заданный
`GRAFANA_ADMIN_PASSWORD`, minikube использует отдельный учебный Secret.

## Замена старого окружения

Новая конфигурация Compose удаляет источник Loki из Grafana provisioning. Если
старый контейнер Loki остался как orphan, остановите именно его через Docker перед
следующим запуском. Старый том с логами не удаляется и не мигрируется автоматически;
VictoriaLogs начинает новый сбор. Исторические отчёты о предыдущих заданиях
сохраняют описание окружения, которое реально проверялось тогда.

## Метрики и предупреждения

Prometheus в Compose собирает метрики API, worker, VictoriaLogs, Tempo, Alloy и себя.
Прикладные метрики показывают HTTP-запросы и задержки, результаты проходов worker,
время последнего успешного прохода и число просроченных счетов. HTTP-маршруты
нормализованы, чтобы идентификаторы счетов не создавали новые временные ряды.

Правила ProcessDown, WorkerStalled и HTTPServerErrors доступны на `/alerts`.
Отправка уведомлений не настроена; Alertmanager и vmalert не подключены.
Бонус задания реализован официальным Grafana-плагином VictoriaLogs.

## Документация

- [VictoriaLogs: Alloy и разбор JSON](https://docs.victoriametrics.com/victorialogs/data-ingestion/promtail/)
- [LogsQL](https://docs.victoriametrics.com/victorialogs/logsql/)
- [Grafana: VictoriaLogs и связь с Tempo](https://docs.victoriametrics.com/victorialogs/integrations/grafana/)
- [Alloy: сбор подов через Kubernetes API](https://grafana.com/docs/alloy/latest/reference/components/loki/loki.source.kubernetes/)
- [OpenTelemetry Go](https://opentelemetry.io/docs/languages/go/)
