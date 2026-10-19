# netbox-ssot: исправление custom fields NetBox 4.7

Версия сборки: **v1.26.1-customfields.1**.
Исходный коммит: [5c833703706ab42efbabe03b037bca10b12478f8](https://github.com/Nectenebis/netbox-ssot-custom-tag-fix/commit/5c833703706ab42efbabe03b037bca10b12478f8).

Исправляет HTTP 400 при синхронизации VM с пользовательскими полями выбора
`fond`, `environment` и аналогичными. PATCH содержит только изменяемые поля.
Нормализация selection/multiselection/object/multiobject учитывает тип поля;
JSON сохраняется без преобразования ключей `id`, `value` и `label`.

## Скачать

- [Linux amd64 (x86_64)](netbox-ssot_v1.26.1-customfields.1_linux_amd64.tar.gz?raw=true)
- [Linux arm64 (aarch64)](netbox-ssot_v1.26.1-customfields.1_linux_arm64.tar.gz?raw=true)
- [SHA256SUMS](SHA256SUMS)
- [Инструкция сборки, установки и отката](INSTALL.md)

Сборки статические, CGO отключён, Go 1.26.9. В каждом архиве находятся
`netbox-ssot`, `INSTALL.md` и `BUILD.json` с точным исходным коммитом и архитектурой.
Перед установкой обязательно проверьте SHA-256. Серверная установка автоматически
не менялась. Не заменяйте конфигурацию, SQLite mapping или systemd preflight.

## Проверки исходного коммита

- golangci-lint v2.14.0: 0 замечаний.
- Полный `go test -race -count=1 -covermode=atomic ./...`: 339 тестов верхнего
  уровня прошли, 2 завершились ошибкой (`TestLookup`, `TestReverseLookup`).
  Публичный DNS в облачном окружении недоступен; эти ошибки наблюдались до
  исправления. Тесты не отключались.
- Регрессионные проверки полей, приоритетов и двух проходов AddVM через mock API
  прошли; VM ID сохранён, повторный PATCH отсутствует.
- Linux amd64: бинарник запускается, `-h` показывает версию и исходный коммит.
- Linux arm64: кросс-сборка и проверка ELF/build metadata; запуск на ARM64
  не проверялся.
- Live NetBox/vCenter недоступны, проверка на реальной инфраструктуре не выполнялась.

Эта ветка хранит только готовые сборки и сведения о них; исходники находятся в
`main`. Бинарники не добавлены в ветку исходников.
