# Включаем интерфейс loopback и смотрим все интерфейсы
ip link set dev lo up
ip a
# Проверяем, что localhost пингуется
ping -c 4 localhost
# Переименуем имя хоста в isolated-env
hostname isolated-env
# Выводим имя пользователя и его UID
whoami
echo $UID
# Выводим ps и имя хоста 
ps
hostname
# Запускаем скрипт из первой части
./direct-run.sh