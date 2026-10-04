# Создадим папку bin, в которую сбилдим наш сервис
mkdir bin

# Сбилдим сервис и асинхронно запустим его
go -C src/api build -o ../../bin/api
bin/api &

# Получим ответ от health endpoint'а сервиса и выведем ps
echo $(curl -fsS http://localhost:8080/health)
ps

# Получим ps сервиса, убъём его и удалим папку bin
pid=$(pgrep api)

echo $pid
kill $pid

rm -rf bin

# Получим воспроизводимый эксперимент по запуску сервиса и его проверке