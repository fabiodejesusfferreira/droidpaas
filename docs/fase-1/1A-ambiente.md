# Bloco 1A — Ambiente (Termux, pacotes, phantom killer, bateria)

Cobre as atividades 1.1 e 1.2 do TSD e as hipóteses **V-08** (phantom killer), **V-14** (Go ≥ 1.22) e a checagem antecipada de **V-01** e **V-09**.

> Os comandos abaixo são o caminho conhecido, mas variam com a versão do Android/One UI. Onde o comando for diferente no seu aparelho, **anote o que funcionou** em [poc-report.md](poc-report.md). Esse é o resultado do bloco.

## 1. Instalar o Termux (premissa P-01)

1. Instale **pelo F-Droid ou GitHub releases**, nunca pela Play Store: Termux, **Termux:API** e **Termux:Boot**.
2. Os três precisam vir da **mesma fonte** (mesma assinatura), senão o Termux:API não conversa com o Termux.
3. Abra o Termux:Boot **uma vez** (sem isso ele não roda no boot) e o Termux:API uma vez.

## 2. Registrar o aparelho

```bash
getprop ro.product.model
getprop ro.build.version.release
getprop ro.build.version.sdk
uname -a
head -3 /proc/meminfo
```

Anote a saída no topo do `poc-report.md` (modelo, Android, SDK, kernel, RAM).

## 3. Pacotes base

```bash
pkg update && pkg upgrade -y
pkg install -y git openssh runit termux-services termux-api jq caddy cloudflared golang nodejs python android-tools
```

Se algum pacote não existir, **registre qual** (impacta V-04/V-05/V-14) e instale o resto separadamente.

## 4. Verificações rápidas

```bash
go version                       # V-14: precisa ser >= 1.22
which sv chpst svlogd runsvdir   # V-09: os quatro devem aparecer
termux-battery-status            # V-01: JSON com percentage e temperature
termux-wake-lock                 # deve mostrar notificação do Termux com wakelock
```

Para o V-01, rode `termux-battery-status` de novo com a **tela apagada** (por SSH ou depois de alguns minutos) e confirme que `temperature` muda/retorna. Se o comando travar, o Termux:API não tem permissão ou foi morto pela bateria.

## 5. Isenção de bateria

1. Configurações do Android → Apps → Termux → Bateria → **Sem restrições** (repita para Termux:API e Termux:Boot).
2. Samsung/One UI: Configurações → Bateria → Limites de uso em segundo plano → **Apps que nunca dormem** → adicione o Termux.
3. Fixe o Termux nos recentes (cadeado).
4. Na notificação do Termux, use **Acquire wakelock** (ou `termux-wake-lock`).

## 6. Phantom process killer (V-08)

O Android 12+ mata processos filhos de apps (limite padrão: 32). Precisa ser desativado via adb.

### 6.1 Depuração sem fio, a partir do próprio Termux

1. Conecte o aparelho ao **Wi-Fi**.
2. Configurações → Opções do desenvolvedor → ative **Depuração sem fio**.
3. Toque em **Parear dispositivo com código de pareamento**. Aparecem um código e `IP:PORTA` de pareamento. Use a **tela dividida/janela flutuante**, porque a janela de pareamento fecha se você sair dela.
4. No Termux:

```bash
adb pair localhost:<PORTA_DE_PAREAMENTO>     # digite o código de 6 dígitos
adb connect localhost:<PORTA_DA_TELA_PRINCIPAL_DE_DEPURACAO_SEM_FIO>
adb devices                                  # deve listar o aparelho como "device"
```

A porta de `connect` é a da tela principal de Depuração sem fio (diferente da de pareamento).

### 6.2 Desativar o monitoramento

Tente na ordem abaixo e **pare no primeiro que funcionar**. Registre qual foi.

**Android 12 / 12L** (e muitos 13):

```bash
adb shell "/system/bin/device_config set_sync_disabled_for_tests persistent"
adb shell "/system/bin/device_config put activity_manager max_phantom_processes 2147483647"
```

**Android 13 (alternativa)**:

```bash
adb shell settings put global settings_enable_monitor_phantom_procs false
```

**Android 14+**: Opções do desenvolvedor → **Desativar restrições de processos filhos** (*Disable child process restrictions*).

### 6.3 Conferir

```bash
adb shell "/system/bin/dumpsys activity settings | grep -i phantom"
```

Deve mostrar `max_phantom_processes=2147483647` (ou o monitor desativado). Se a saída não bater, o comando não pegou na sua versão.

> Atenção: a configuração pode **ser revertida em reboot** ou por atualização do sistema. Reaplique após reiniciar e anote se persistiu (alimenta V-15 e o checklist operacional do TSD §14.4).

## 7. Teste curto de sobrevivência (antes do teste de 24 h)

```bash
sleep 3600 &        # vários processos filhos em segundo plano
for i in $(seq 1 40); do sleep 3600 & done
```

Apague a tela, espere ~10 min e volte:

```bash
jobs | wc -l        # devem continuar todos vivos
```

Se aparecerem `Killed` / `signal 9`, o phantom killer ainda está ativo: volte ao passo 6. Limpe com `kill $(jobs -p)`.

## 8. Saída do bloco

Preencher em [poc-report.md](poc-report.md):

| Item | Onde registrar |
|---|---|
| Modelo/Android/SDK/kernel/RAM | Topo do relatório |
| Pacotes que não existem no Termux | Decisões alteradas |
| V-14 (versão do Go) | Linha V-14 |
| V-09 (`sv`, `chpst`, `svlogd` presentes; `chpst -n` testa em 1B) | Linha V-09 (parcial) |
| V-01 (temperatura com tela apagada) | Linha V-01 |
| V-08 (comando exato que funcionou + resultado do passo 7) | Linha V-08 (parcial; o veredito final vem das 24 h do bloco 1G) |

Depois marque 1A como concluído no checklist de [README.md](README.md) e siga para o bloco 1B (app Node sob `termux-services` + Caddy).
