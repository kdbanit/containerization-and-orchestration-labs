# Лаба 2. А как следить-то?

## Оглавление

0. [Создаём тестовый сервис](#создаём-тестовый-сервис)
1. [Собираем метрики](#собираем-метрики)
2. [Собираем логи](#собираем-логи)
3. [Смотрим трейсы](#смотрим-трейсы)
4. [Настраиваем алерты](#настраиваем-алерты)


### Инфа о том, как это потом использовать в других лабах

Репы с Helm чартами:
- prometheus-community https://prometheus-community.github.io/helm-charts
- grafana https://grafana.github.io/helm-charts
- jaegertracing https://jaegertracing.github.io/helm-charts
- wiremind https://wiremind.github.io/wiremind-helm-charts


Команда для добавления репы `helm repo add <name> <repo>`
Команда для обновления списков реп `helm repo update`

Сами чарты, которые используем в лабе:
- prometheus-community/kube-prometheus-stack
- grafana/loki
- grafana/alloy
- jaegertracing/jaeger
- wiremind/karma

Типовая команда установки нового чарта
```console
helm upgrade --install <name> <repo>/<chart-name> \
  -n <namespace> \
  -f <name>-values.yaml \
  --wait \
  --timeout 5m
```

Пробрасываем через `kubectl port-forward`

## Создаём тестовый сервис

В качестве движка для k8s будем использовать minikube.

Используя LLM сгенерируем сервис по требованиям на языке программирования Go. Исходный код сервиса представлен в [src/api/main.go](src/api/main.go). 

Также напишем Helm Chart для запуска сервиса. Его исходные коды представлены в [src/api/helm](src/api/helm).

## Собираем метрики

ATTENTION: весь мониторинг работает в namespace monitoring, сервис живёт в namespace default. В явном виде namespace не прописаны.

Начнём со сбора метрик. Для этого развернём kube-prometheus-stack, который включает в себя Grafana, Prometheus и Alertmanager. Для работы с Prometheus настроен ServiceMonitor, исходный код шаблона которого находится в [src/api/helm/templates/servicemonitor.yaml](src/api/helm/templates/servicemonitor.yaml).

Собран дашборд по RED, который строит графики по следующим метрикам:
- sum(rate(http_requests_total{job="monitoring-api"}[2m])) - среднее количество запросов в секунду за последние две минуты
- 100 * sum(rate(http_requests_total{job="monitoring-api",status=~"5.."}[2m]))/clamp_min(sum(rate(http_requests_total{job="monitoring-api"}[2m])), 0.000001) - процент количества серверных ошибок относительно всех запросов за последние две минуты
- histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{job="monitoring-api"}[5m]))) - p95 времени ответа на запросы по гистограмме за последние пять минут

Скриншот дашборда приведен ниже, как видно графики строятся.

![Наконец-то начинаем следить за сервисом](images/screenshots/1.png)

## Собираем логи

Теперь, когда у нас есть Grafana, нужно прикрутить к ней Loki, а к нему агент сбора логов Alloy. 

Параметры для развёртывания Helm Chart с Loki находятся в [src/helm/loki-values.yaml](src/helm/loki-values.yaml), для Helm Chart с Alloy - [src/helm/alloy-values.yaml](src/helm/alloy-values.yaml).

Скриншот с пойманной ошибкой находится ниже.

![Ловцы ошибок](images/screenshots/2.png)

## Смотрим трейсы

Теперь поднимем Jaeger и подключим к нему наш сервис. Параметры для развёртывания находятся в [src/helm/jaeger-values.yaml](src/helm/jaeger-values.yaml). Для работы с Jaeger сервису необходимо передать переменную окружения OTEL_EXPORTER_OTLP_ENDPOINT.
В сервисе изначально было предусмотрена поддержка OpenTelemetry, все запросы имеют trace_id и span_id. Посмотрим на /slow в трейсах.

![slow в трейсе](images/screenshots/3.png)

Видно, что у него есть второй span, который забирает на себя почти всё время выполнения.

Посмотрим на /fail в трейсах.

![fail в трейсе](images/screenshots/4.png)

Видно, что он имеет exception event внутри себя.

Посмотрим на /load в трейсах.

![load в трейсе](images/screenshots/5.png)

Видно, что он вызывает множество дополнительных событий как клиент.

Возьмём trace_id slow запроса из Grafana и найдём его в Jaeger. 

На скриншотах ниже представлены результаты сопоставления.

![трейс в grafana](images/screenshots/6.png)

![трейс в jaeger](images/screenshots/7.png)


## Настраиваем алерты


Придумаем три правила алертинга и опишем их в [src/alerts/api-alerts.yaml](src/alerts/api-alerts.yaml).

В нашем случае это будут следующие правила:
- APIHighErrorRate (больше 10 процентов 500x запросов за последние две минуты, может говорить о том, что внутри сервиса что-то сломалось, либо у сервиса отвалилось соединение с чем-то внешним. Надо смотреть конфигурацию сервиса и проверять, все ли соединения сервиса с внешними datasource в порядке);
- APIHighLatency (p95 за последние пять минут больше двух секунд, либо у сервиса есть эндпоинты с долгим выполнением, которые массово дёрнули, либо сервис подвис и его не хватает ресурсов, ну или соединения с внешним миром медленные. В первую очередь нужно идти в Jaeger, затем можно смотреть сколько ресурсов сервису выделено, ну и коннекты наружу, естественно);
- APIDown (сервис недоступен в течение минуты или больше. Так как мониторим внутри кластера, то либо сервис реально упал, либо кластер начал помирать. Если сервис упал разбираемся в причинах данного явления, если кластер помирает, то кого-то ждёт весёлая смена).

Проверим работу алертов и их отображение в Karma. 
(Интересное наблюдение: полезные инструменты по типу кармы выглядят максимально непривлекательно внешне. Наследие от SCADA и подобного?)

Уроним наш сервис, снизив количество реплик до нуля.

`kubectl scale deployment monitoring-api -n default --replicas=0`

В Karma увидим следующий алерт

![упали](images/screenshots/8.png)

Для следующих экспериментов сервис был rollout'нут

`kubectl rollout status deployment/monitoring-api -n default`

Теперь попробуем наспамить slow запросами, чтобы вызвать high latency.

![наспамили](images/screenshots/9.png)

Как видно, high latency алерт отработал отлично.

Осталось проверить работу алерта на отлов процента ошибок.

![тонна ошибок](images/screenshots/10.png)

Как видно, все алерты отработали. А раз мы их увидели в Karma, то и в Prometheus они были в Firing.

Считаю, что лабораторная работа на этом моменте может быть закончена.
