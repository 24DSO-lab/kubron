# Установка Desktop-версии

Подробности также доступны в англоязычной инструкции: [Desktop installation](/guide/installation).

Кратко для Linux:

```bash
sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0   # runtime
# для сборки из исходников:
sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev
```

Сборка:

```bash
git clone https://github.com/24DSO-labs/kubron.git
cd kubron
wails doctor
wails build -tags webkit2_41   # на Linux
```
