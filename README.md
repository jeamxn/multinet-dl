# MultiNet Downloader (`mndl`)

파일 하나를 **네트워크 여러 개(Wi-Fi·아이폰 테더링·유선·USB 랜)로 쪼개서 동시에** 받는 다운로더.
네트워크마다 aria2c처럼 연결을 여러 개 열고, 빠른 네트워크가 남은 조각을 알아서 더 가져간다.
macOS · Windows · Linux.

- **CLI(`mndl`)가 본체**고, 데스크톱 앱은 그 CLI를 실행해서 조절하는 화면일 뿐이다.
  CLI와 앱은 같은 설정 파일을 같이 쓴다.
- 이어받기: 중간에 끊기거나 꺼도 같은 명령으로 다시 실행하면 받은 데부터 이어서 받는다 (`.mndl.part` + `.mndl.json`).
- 네트워크 하나가 끊겨도 나머지 네트워크가 마저 받는다.
- 서버가 연결 수를 막으면(429/503) 연결을 알아서 줄인다.
- 서버가 Range를 지원 안 하면 한 줄로 받는다.
- 같은 이름 파일이 있으면 덮어쓰지 않고 `이름 (1).ext`로 저장한다.

> 네트워크를 여러 개 쓰려면 **진짜 인터페이스가 여러 개** 있어야 한다. 맥·노트북 내장 Wi-Fi 칩은 한 번에 와이파이 하나에만 붙는다.
> 아이폰 USB 테더링, USB 랜/이더넷, 와이파이→랜 트래블 라우터 등을 붙이자.
> 같은 공유기에 여러 개 붙이는 건 회선이 같아서 대부분 효과가 없다.

## 설치

[Releases](../../releases)에서 받는다.

| OS | 앱 | CLI만 |
|---|---|---|
| macOS (Intel·Apple Silicon) | `MultiNet-Downloader-macos.dmg` | `mndl-darwin-universal` |
| Windows x64 / ARM64 | `MultiNet-Downloader-windows-<arch>.zip` (exe 옆에 `mndl.exe`) | `mndl-windows-<arch>.exe` |
| Linux x64 / ARM64 | `MultiNet-Downloader-linux-<arch>.tar.gz` | `mndl-linux-<arch>` |

- macOS: 서명·공증 안 된 앱이라 처음엔 우클릭 → 열기 (또는 `xattr -dr com.apple.quarantine "/Applications/MultiNet Downloader.app"`).
  CLI는 앱 안 `MultiNet Downloader.app/Contents/MacOS/mndl` 에도 들어 있다.
- Linux 앱은 GTK3 + WebKit2GTK 4.1이 필요하다 (`libwebkit2gtk-4.1-0`).
  인터페이스를 확실히 고정하려면 `sudo setcap cap_net_raw+ep ./mndl` (없으면 출발 주소만 고정해서, 라우팅 설정에 따라 다른 네트워크로 나갈 수 있음).

## CLI

```bash
mndl nets                 # 쓸 수 있는 네트워크 (--all: VPN·가상 포함, --json)
mndl test                 # 네트워크별로 인터넷 되는지 + 공인 IP
mndl get URL              # 설정된 네트워크 전부로 받기
mndl get -n en0,en7 -c 16 -d ~/Downloads URL
mndl get -o name.iso -H "Cookie: a=b" URL
mndl get URL1 URL2 URL3   # 여러 개는 차례대로

mndl config               # 기본값 보기
mndl config dir ~/Downloads
mndl config networks en0,en7
mndl config conns 16
mndl discard 이름          # 이어받기용 임시 파일 지우기
```

| 옵션 | 뜻 |
|---|---|
| `-n, --net` | 쓸 네트워크(쉼표). ID(`en0`, `Wi-Fi 2`, `wlan0`)나 이름. 없으면 설정값, 그것도 없으면 물리 네트워크 전부 |
| `-c, --conns` | 네트워크당 동시 연결 수 1~64 (aria2c `-x`, aria2c는 16이 한계) |
| `-d, --dir` | 저장 위치 |
| `-o, --out` | 파일 이름 (URL 하나일 때) |
| `-H, --header` | 요청 헤더 추가 (여러 번) |
| `--json` | 진행 상황을 JSON 한 줄씩 stdout으로. stdin으로 `pause` / `cancel` 을 받는다 (앱이 이걸 씀) |

- `Ctrl+C` = 일시정지(임시 파일 남김). 같은 명령을 다시 치면 이어받는다.
- 종료 코드: 0 성공 · 1 실패 · 130 일시정지/취소.
- 설정 파일: macOS `~/Library/Application Support/multinet-dl/config.json`, Windows `%AppData%\multinet-dl\config.json`, Linux `~/.config/multinet-dl/config.json` (`MNDL_CONFIG` 로 바꿀 수 있음).

연결 수는 16개 넘게 올리면 서버가 막거나(429) 차단할 수 있다. 막히면 알아서 줄이긴 하지만, 보통 8~16이면 충분하다.

## 앱

왼쪽에서 네트워크를 고르고(연결 확인 버튼으로 실제 인터넷이 되는지·공인 IP 확인), 연결 수와 저장 위치를 정한 뒤
주소를 붙여넣고 다운로드. 주소는 여러 줄로 여러 개 넣을 수 있고, 브라우저에서 링크를 끌어다 놓아도 된다.
진행 막대는 네트워크별 색으로 누가 얼마나 받았는지 보여준다. 항목마다 실제로 실행한 CLI 명령을 볼 수 있다.
앱을 닫으면 받던 건 일시정지되고, 다음에 열어서 ▶ 누르면 이어받는다.

## 동작 원리

1. 첫 바이트를 `Range: bytes=0-0`으로 요청해서 크기·Range 지원·ETag를 확인한다.
2. 파일을 `네트워크 수 × 연결 수` 조각으로 나누고, 조각마다 고른 네트워크에 묶인 연결로 받는다.
   - macOS `IP_BOUND_IF` / Windows `IP_UNICAST_IF` / Linux `SO_BINDTODEVICE` 로 소켓을 인터페이스에 고정 → 그 네트워크의 게이트웨이로 나간다.
   - HTTP/2는 끈다 (연결 하나로 합쳐지면 나눠 받는 의미가 없어서).
3. 일이 끝난 연결은 가장 큰 남은 조각의 뒤쪽 절반을 가져간다 → 빠른 네트워크가 더 많이 받는다.
4. 1초마다 남은 구간을 `.mndl.json`에 저장해서 언제 꺼져도 이어받을 수 있고, ETag/Last-Modified가 바뀌면 처음부터 받는다.

## 빌드

Go 1.23+ 와 [Wails v2](https://wails.io) 가 필요하다.

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0
go test ./internal/...
scripts/build.sh            # 지금 OS용 앱 + CLI → dist/
scripts/build.sh windows    # 맥/리눅스에서 윈도우용 교차 빌드
scripts/build.sh cli        # CLI만 전 플랫폼
```

`v*` 태그를 푸시하면 GitHub Actions가 세 OS를 빌드해서 Release에 올린다.

## 라이선스

[GPL-3.0-or-later](LICENSE)
