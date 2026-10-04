# Выводим UID пользователя, от которого запустили скрипт
echo $UID
# Создаём окружение через unshare и запускаем в нём тестовый скрипт
unshare \
    --user --map-root-user --pid \
    --mount --net --uts \
    --ipc --fork --mount-proc \
    /bin/bash test-namespaces.sh
# Ждём "смерти" unshare для получения PID fork'нутого процесса внутри окружения
sleep 0.5
pid=$!
# Выводим ps
ps
# Выводим полученный ранее PID
echo $pid