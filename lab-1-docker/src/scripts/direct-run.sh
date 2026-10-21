#!/usr/bin/env bash
# Создадим папку bin, в которую сбилдим наш сервис
if [ ! -d bin ]; then
    mkdir bin
fi

# Сбилдим сервис и асинхронно запустим его
if [ ! -f bin/api ]; then
    go -C src/api build -o ../../bin/api
fi

bin/api &

# Получим ответ от health endpoint'а сервиса и выведем ps
echo $(curl -fsS http://localhost:8080/health)
ps

# Получим PID сервиса и убъём его
pid=$(pgrep api)

echo $pid
kill $pid
