# Printf Upload Optimization — Auto-fallback lineSize

**Date:** 2026-07-16
**Status:** approved

## Problem

При доставке файлоадера (~6 KB бинарник) на целевое устройство через printf, каждая команда отправляется через stop-and-wait telnet `Execute()`. Текущий `lineSize = 128` байт означает ~50 round-trips, что на медленных устройствах (HiSilicon ARMV5, ~340ms/round-trip) даёт ~17s оверхеда.

## Design decision

- **Батчинг printf через `sh -c` отвергнут** — экономия +1.3s не окупает сложность (quote escaping, разная длина команды на разных устройствах, потеря контекста при ошибке)
- **Увеличение `lineSize` до 1024** даёт 94% выигрыша (17s → 2s, 8×)
- **Авто-fallback** решает проблему неизвестных устройств с меньшим лимитом длины команды

## Design

### Auto-fallback loop

```go
lineSizes := []int{1024, 512, 256, 128}

for _, lineSize := range lineSizes {
    tc, err := newTelnetClient()
    if err != nil { return err }

    err = uploadData(tc, fileloaderData, remotePath, lineSize)
    tc.Close()

    if err == nil {
        break // success — working lineSize found
    }
    // error → try next smaller lineSize
}
```

- Каждая итерация открывает **новое** telnet-соединение (предыдущее могло быть в неопределённом состоянии после ошибки)
- Последний `128` гарантированно совместим (текущий дефолт)
- На SStar (и любом устройстве с лимитом ≥ 1024): первая попытка успешна
- На устройстве с лимитом < 1024: одна лишняя попытка (~2s на HiSilicon) → fallback

### Изменения в коде

**`internal/helpers/upload.go`:**
- `UploadData()` принимает `lineSize int` параметром вместо константы
- Константа `defaultLineSize = 128` сохраняется для printf-fallback пути (большие файлы за NAT)

**`internal/xfer/push.go`, `internal/xfer/pull.go`:**
- Добавить цикл fallback перед загрузкой файлоадера
- После успешной загрузки — продолжить как раньше (`chmod +x`, TCP listener)

### Что НЕ меняется

- Формат printf-команд (octal `\NNN`, `>` / `>>`)
- `tc.Execute()` — без изменений
- Printf-fallback для передачи файлов (`scout.Push()` / `pull_printf.go`) — остаётся 128

### Отказ от кэширования

Кэширование рабочего `lineSize` на устройство не нужно:
- Файлоадер загружается один раз за сессию
- Probing стоит максимум 1 лишнюю попытку на проблемных устройствах
- Нет состояния, которое нужно синхронизировать между сессиями

## Expected results

| Устройство | До (128) | После (1024) | fallback |
|-----------|----------|--------------|----------|
| SStar ARMV7 | 3.3s | 0.4s | 1-я попытка |
| HiSilicon ARMV5 | 17.0s | 2.0s | 1-я попытка (если 1024 OK) |
| Unknown, limit=512 | ~8s | ~4s | 2-я попытка |
| Unknown, limit=128 | ~17s | ~17s | 4-я попытка |
