# Отчет HW4: VictoriaMetrics

Николай Хадзакос, НИУ ВШЭ, nakhadzakos@edu.hse.ru

## Конфигурация

Приложение управления платёжными счетами: Go, HTTP API, фоновый worker,
PostgreSQL 17. Доменная модель не зависит от PostgreSQL, HTTP и клиентской
библиотеки метрик. Сервис получает порт `InvoiceMetrics`; адаптер на
`prometheus/client_golang` подключается в `cmd/payments`.

В minikube установлен официальный Helm-чарт
`victoria-metrics-single` версии `0.49.0`: релиз `vm-single`, namespace `observability`.
VictoriaMetrics хранит данные на PVC 2 GiB, срок хранения — семь дней.
Встроенный сборщик выполняет pull каждые десять секунд; отдельный vmagent не нужен.
Релиз имеет статус `deployed`, revision 2. При проверке обнаружены шесть целей:
три API, два worker и один postgres-exporter. Все цели имеют статус `up`;
метрика `pg_up{namespace="sre-payments"}` равна 1.

## Экспортеры

- API и worker: `prometheus/client_golang`, готовые GoCollector и ProcessCollector.
  Экспортируются горутины, память, GC, CPU и другие метрики процесса.
- `/metrics` поддерживает Prometheus text и OpenMetrics по заголовку `Accept`.
  Авторизация не требуется. Service приложения — ClusterIP, ingress и NodePort
  для метрик не создаются; локальная демонстрация использует port-forward.
- PostgreSQL: готовый `quay.io/prometheuscommunity/postgres-exporter:v0.19.1`,
  второй контейнер пода `postgres-0`. Подключается к `localhost:5432`, отдаёт
  метрики на `9187/metrics`. Пароль берётся из существующего Secret `payments-db`.
  Для учебного стенда используется существующая учётная запись БД; в рабочем
  окружении следует выдать отдельному пользователю необходимые права мониторинга.

## Service discovery

Аннотации `prometheus.io/scrape`, `prometheus.io/port`, `prometheus.io/path`
заданы в шаблонах подов Deployments API/worker и StatefulSet PostgreSQL.
Через Kubernetes API VictoriaMetrics обнаруживает поды namespace `sre-payments`.
Role разрешает только get/list/watch pods в этом namespace; RoleBinding связывает
её с ServiceAccount `observability/vm-single-scraper`. Доступ к Secrets не выдаётся.

Отбираются работающие поды с `scrape=true`, исключаются init-контейнеры,
порт контейнера сверяется с аннотацией. Это исключает попытку скрейпинга
PostgreSQL на порту 5432 и дублирование метрик экспортера.
Временные ряды содержат `namespace`, `pod`, `app`, `container`.
Пересоздание и масштабирование подов не требуют ручного изменения адресов.

## Бизнес-метрики

| Имя | Тип | Когда увеличивается |
| --- | --- | --- |
| `payments_invoices_created_total` | Counter | Успешно сохранён новый черновик счёта |
| `payments_invoices_issued_total` | Counter | Успешно сохранён переход draft → open |
| `payments_invoices_cancelled_total` | Counter | Успешно сохранён переход open → cancelled |
| `payments_invoices_expired_total` | Counter | Успешно сохранён переход open → expired |

Счётчики увеличиваются после успешного завершения операции репозитория,
который сохраняет счёт и событие истории в одной транзакции. Ошибки валидации,
конфликты версий и ошибки записи их не увеличивают. Счётчик expired учитывает
успешные переходы даже при последующей ошибке в том же проходе worker.
Идентификаторы счетов не включены в labels.

Счётчики локальны процессу и сбрасываются при перезапуске. Для скорости изменения
используется `sum(rate(payments_invoices_created_total[5m]))`, для числа событий
за интервал — `sum(increase(payments_invoices_created_total[1h]))`.
Это операционные метрики, а не финансовый реестр: между commit и увеличением
счётчика возможна остановка процесса, а между scrape — потеря ещё не собранных
значений при перезапуске. Источником истины остаются БД и история событий.

Дополнительно сохранены технические HTTP-метрики, длительность запросов,
результаты проходов worker и время его последнего успешного прохода.

## Файлы

- `deploy/minikube/` — приложение, PostgreSQL с exporter, аннотации discovery.
- `deploy/observability/victoriametrics/values.yaml` — настройки Helm и scrape-конфигурация.
- `deploy/observability/victoriametrics/access.yaml` — Role и RoleBinding для discovery.
- `internal/telemetry/`, `internal/service/`, `internal/worker/`, `cmd/payments/` —
  реализация и подключение метрик.

В корне архива находятся этот отчёт под именем `Отчёт.md` и запись
`Скринкаст.mov`. Исходный код приложения, Dockerfile и файлы зависимостей
также находятся в архиве; frontend встроен в Go-приложение.

Для VictoriaMetrics дополнительно приложены:

- `deploy/observability/victoriametrics/victoria-metrics-single-0.49.0.tgz` —
  официальный Helm-чарт с зависимостями.
- `deploy/observability/victoriametrics/installed-manifests.yaml` —
  манифесты установленного Helm-релиза, полученные через `helm get manifest`.

Файлы Helm не включены в `kustomization.yaml`: установка VictoriaMetrics
выполняется отдельно от Kustomize командами из README.

## Источники

- [Официальная инструкция VictoriaMetrics](https://docs.victoriametrics.com/guides/k8s-monitoring-via-vm-single/)
- [Helm-чарт VictoriaMetrics single](https://github.com/VictoriaMetrics/helm-charts/tree/master/charts/victoria-metrics-single)
- [Postgres exporter](https://github.com/prometheus-community/postgres_exporter)
- [Go client](https://github.com/prometheus/client_golang)
