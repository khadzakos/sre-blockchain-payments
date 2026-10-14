# ДЗ №3 — логи в VictoriaLogs

Николай Хадзакос, НИУ ВШЭ, nakhadzakos@edu.hse.ru

## Что получилось

В тот же minikube-кластер `sre-payments`, где уже работает приложение,
добавлены VictoriaLogs single-node, сборщик Alloy, Tempo и Grafana.
Приложение осталось в namespace `sre-payments`, инфраструктура живёт в `observability`.
При проверке все четыре пода инфраструктуры были готовы, API работал в трёх
репликах, worker — в двух.

VictoriaLogs в логи и трейсы хранятся
семь дней; VictoriaLogs и Tempo используют постоянные тома по 2 GiB.

## Как едут логи и трейсы

```text
Приложение → JSON в stdout → Alloy → VictoriaLogs
Приложение → OpenTelemetry → Alloy → Tempo
VictoriaLogs + Tempo → Grafana
```

Приложение пишет логи через zap. В JSON есть `timestamp`, `level`, `message`,
`service.name` и `service.version`. Для HTTP-запросов добавляются `request_id`,
`trace_id`, `span_id`, метод, маршрут, статус и длительность.

Alloy читает логи подов API, worker и миграций через Kubernetes Pod logs API
и отправляет их в Loki-совместимый endpoint VictoriaLogs
`/insert/loki/api/v1/push`.
Параметр `_msg_field=message,msg` сохраняет текст сообщения в поле `_msg`.
При сбоях сборщик повторяет отправку, но недоставленные данные могут потеряться;
после перезапуска возможны повторы записей.

## Как запустить и открыть

Манифесты Deployment, Service и PVC лежат в [deploy/observability](../deploy/observability/).
Они объединены через **Kustomize**; конфигурацию к работающему приложению можно применить
командой `kubectl --context=sre-payments apply -k deploy/observability`.
При изменении генерируемых ConfigMap Deployment обновляет поды автоматически.
При уже работающем Docker весь стенд запускается из корня проекта одной командой:

```sh
make minikube-up
```

Команда запускает minikube, приложение и инфраструктуру, а локальные подключения
к UI открывает в фоне. VictoriaLogs доступна внутри кластера по
`http://victorialogs.observability.svc.cluster.local:9428`.

- vmui: <http://127.0.0.1:9428/select/vmui/>. В установленной версии v1.53.0 используется этот путь; короткий `/vmui/` возвращает ошибку.
- Grafana: <http://127.0.0.1:13000>, учебный логин и пароль — `admin` / `minikube-grafana-password`.

## Четыре запроса в vmui

Все запросы выполнены через веб-интерфейс. Окно — последние 15 минут,
поэтому при повторном запуске числа будут другими.

**1. Последние сырые записи**

```logsql
_time:15m project:="sre-payments" | sort by (_time) desc | limit 20
```

Получены 20 последних записей API и worker: `http request` и `operation finished`.

**2. Фильтр по полям: HTTP-ответы со статусом от 400**

```logsql
_time:15m project:="sre-payments" service.name:="payments-api" _msg:="http request" status:>=400 | fields _time, _msg, status, route, request_id
```

Для проверки запросил отсутствующий счёт и передал недопустимый `limit`.
В результатах появились ответы 404 и 400. Эти события имеют уровень `info`,
поэтому здесь удобнее фильтровать по статусу.

**3. Stats: сколько логов у каждого сервиса и уровня**

```logsql
_time:15m project:="sre-payments" | stats by (service.name, level) count() as logs
```

Получены группы API и worker по уровням `info` и `warn` с количеством записей.

**4. Top: самые частые HTTP-маршруты**

```logsql
_time:15m project:="sre-payments" service.name:="payments-api" route:* | top 5 by (route)
```

В рейтинге видны `/readyz`, `/healthz`, `/api/invoices` и `/api/invoices/{id}`.
Проверки готовности и здоровья дают больше всего запросов.
`count()` и `top` считают записи, включая возможные повторы от сборщика.

## Grafana и поиск одного запроса

Подключён официальный плагин `victoriametrics-logs-datasource`.
Есть дашборд **Payments · Logs & traces** с логами, HTTP-ошибками,
числом записей по сервисам и популярными маршрутами.

По `request_id` можно найти логи одного запроса, по `trace_id` — связанные
операции. Из лога ссылка **Open trace** открывает трейс в Tempo;
из span ссылка **Related logs** возвращает логи с фильтром по `trace_id` и `span_id`.
Оба перехода проверены через Grafana.

Для GET `/api/invoices` с `request_id=64bb87de2c59da7751e9b9a4`
и `trace_id=865f76b759ffe3bf040124b795b78102` в Tempo найдены три span:
`GET /api/invoices`, `service.List` и `postgres.select`.
Идентификаторы совпали с логами; трейс сохранился после замены пода Tempo.

