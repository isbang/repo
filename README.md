# repo

GitHub에서 접근 가능한 레포지토리 목록을 **XDG 캐시**에 저장해 두고, **퍼지 검색**으로 골라 **clone** 하는 CLI입니다.
실행할 때마다 캐시 갱신을 **백그라운드**로 돌리기 때문에 피커는 항상 즉시 열립니다.
엔터를 누르면 **클론 큐**에 들어가 백그라운드에서 clone되고 피커는 그대로 열려 있어서, 한 번 실행으로 여러 레포를 받을 수 있습니다.

```
$ repo
❯ act
  3/66 · synced 12s ago · refreshing…   queued isbang/action-test → /home/ilsub/ws/action-test
  ✓ isbang/dsn                      ×3 Go
❯ ⠹ isbang/action-test                 private
  · hoverkraft-tech/compose-action     TypeScript  This action runs your docker-compose file…

  queue 3 · 1 cloning · 1 waiting · 1 done
  ⠹ isbang/action-test   2s
  · hoverkraft-tech/compose-action
  ✓ isbang/dsn           800ms → /home/ilsub/ws/dsn
  enter queue clone · ctrl+r refresh · ↑↓/ctrl+p,n move · esc quit
```

## 설치

```bash
make install            # ~/.local/bin/repo  (PREFIX=/usr/local 등으로 변경 가능)
# 또는
go install github.com/isbang/repo@latest
```

`git`이 PATH에 있어야 clone이 동작합니다.

## 인증

토큰을 다음 순서로 찾습니다. `gh auth login`만 해두면 별도 설정이 필요 없습니다.

1. `$REPO_GITHUB_TOKEN`, `$GH_TOKEN`, `$GITHUB_TOKEN`
2. `gh auth token --hostname <host>` (gh가 키링에 저장한 토큰도 이 경로로 읽습니다)
3. gh의 `hosts.yml` 안 `oauth_token`

필요 권한은 `repo`(private 레포 조회) 정도입니다.

## 사용법

| 명령 | 설명 |
| --- | --- |
| `repo` | 인터랙티브 피커 → 엔터로 클론 큐에 넣고, 백그라운드에서 현재 디렉터리에 clone |
| `repo clone [query...]` | 쿼리로 바로 clone (모호하면 피커가 쿼리를 채운 채 열림) |
| `repo search <query...>` | 퍼지 검색 결과를 점수 순으로 출력 |
| `repo list` | 캐시된 목록 출력 (파이프용) |
| `repo sync` | 캐시를 포그라운드에서 갱신 |
| `repo cache path\|info\|clear` | 캐시 위치·상태 확인, 삭제 |
| `repo config path\|show\|init` | 설정 파일 확인·생성 |
| `repo completion <shell>` | 셸 자동완성 스크립트 출력 |

### clone

```bash
repo                                  # 피커에서 여러 개 큐에 넣기
repo clone kube-tools                 # 이름이 유일하면 바로 clone (포그라운드)
repo clone isbang/repo -C ~/workspace # 부모 디렉터리 지정
repo clone repo -o my-repo            # clone 디렉터리 이름 지정
repo clone repo --protocol ssh
repo clone repo -- --depth 1 --recurse-submodules   # -- 뒤는 git에 그대로 전달
repo clone repo --print-path          # clone 경로를 stdout으로 (cd $(repo clone ... --print-path))
repo clone repo --dry-run             # 실행될 git 명령만 출력
repo -j 4                             # 동시 clone 4개까지
```

쿼리 해석 규칙:

- `owner/name` 완전 일치, 유일한 레포 이름 일치, 또는 매치가 1건 → **바로 clone**(포그라운드, git 출력 그대로 표시)
- 여러 개 매치 → 터미널이면 피커를 쿼리 채운 상태로 열고, 아니면 후보를 보여주며 실패 (`--first`로 1등 선택)
- `-s/--select` → 쿼리가 있어도 항상 피커

### 클론 큐

피커에서 엔터를 누르면 바로 clone이 시작되지 않고 **큐에 적재**됩니다. 워커(기본 2개, `-j`/`clone_concurrency`로 조절)가
백그라운드에서 `git clone`을 돌리고, 피커는 열린 상태로 진행 상황을 보여줍니다. 그래서 한 번 실행으로 여러 레포를 받을 수 있습니다.

- 엔터 → 큐 적재 후 커서가 한 칸 내려가므로 연속으로 여러 개를 고를 수 있습니다.
- 각 행 앞의 표시: `·` 대기, `⠹` clone 중, `✓` 완료, `✗` 실패, `⊘` 건너뜀(이미 존재하거나 dry-run).
- 같은 레포를 두 번 넣거나, 이미 있는 디렉터리·다른 작업과 같은 경로면 그 자리에서 거절하고 이유를 상태줄에 띄웁니다.
- `esc`로 피커를 닫으면 남은 clone을 기다린 뒤 결과를 요약합니다. 진행 중 `ctrl+c`는 clone을 중단합니다.
- 백그라운드 clone은 터미널을 쓸 수 없으므로 `GIT_TERMINAL_PROMPT=0`으로 실행합니다(자격증명 프롬프트에서 멈추지 않고 실패).
- git 출력은 캡처되어 실패한 작업의 마지막 줄만 요약에 표시됩니다.

### 검색 / 목록

```bash
repo search kube --limit 5
repo search isbang cli --score        # 공백으로 나눈 term은 AND
repo list --owner isbang --private
repo list --sort clones --long       # 최근에 많이 clone한 순서
repo list --json | jq -r '.[].ssh_url'
repo list --forks=false --archived=false
```

### 정렬 순서

기본 순서(검색어 없음)는 **최근 클론 횟수 많은 순 → 이름 순**입니다.
"최근"은 최근 30일 안에 있는 클론 이력 중 **마지막 100건**을 뜻하고, 이 안에서 레포별로 횟수를 셉니다.
2회 이상 clone한 레포는 피커에 `×3`처럼 횟수가 함께 표시됩니다.
검색어를 입력하면 퍼지 점수가 1순위, 클론 횟수가 동점 처리 기준입니다.

퍼지 매칭은 fzf 스타일입니다. 부분 문자열이 아니라 **순서만 맞으면** 매치되고(`kt` → `kube-tools`),
단어 경계(`/ - _ .`, camelCase)와 연속 매치에 가점, 간격에 감점을 줍니다.
`owner/name`에서 `/` 바로 뒤(레포 이름 시작)가 가장 높은 가점을 받으므로 보통 원하는 레포가 위로 옵니다.
쿼리에 대문자가 없으면 대소문자를 구분하지 않습니다(smart case).

### 피커 키

| 키 | 동작 |
| --- | --- |
| 입력 | 퍼지 검색 |
| `enter` | 클론 큐에 넣고 다음 행으로 |
| `↑`/`↓`, `ctrl+p`/`ctrl+n`, `ctrl+k`/`ctrl+j` | 이동 |
| `pgup`/`pgdown`, `home`/`end` | 페이지·처음·끝 |
| `ctrl+r` | 캐시 갱신 요청 |
| `esc`, `ctrl+c` | 피커 종료 (큐가 비어 있으면 종료 코드 130) |

백그라운드 갱신이 끝나면 피커가 **열려 있는 상태에서** 목록을 자동으로 다시 읽어옵니다(선택 위치 유지).

## 자동완성

레포 이름이 캐시에서 동적으로 완성됩니다. 이름이 유일하면 `owner/` 없이 짧게, 중복이면 `owner/name`으로 제시하고
설명·private 여부를 함께 보여줍니다. `--owner`는 캐시의 소유자 목록으로, `--protocol`/`--sort`는 고정 값으로 완성됩니다.
완성 경로에서는 백그라운드 갱신도 네트워크 요청도 하지 않습니다(항상 로컬 캐시만 읽으므로 즉시 응답).

```bash
# bash (bash-completion 필요)
repo completion bash > ~/.local/share/bash-completion/completions/repo

# zsh  ($fpath 안의 디렉터리에, compinit 전에 위치)
repo completion zsh > "${fpath[1]}/_repo"

# fish
repo completion fish > ~/.config/fish/completions/repo.fish

# 현재 셸에서 바로 테스트
source <(repo completion bash)
```

`make completions`로 `./completions/`에 세 셸 스크립트를 한 번에 만들 수도 있습니다.

## 캐시와 파일 위치

XDG Base Directory 규칙을 따릅니다(환경변수가 절대경로가 아니면 무시하고 기본값 사용).

| 용도 | 경로 |
| --- | --- |
| 레포 목록 캐시 | `$XDG_CACHE_HOME/repo/repos.json` (기본 `~/.cache/repo/repos.json`) |
| 설정 | `$XDG_CONFIG_HOME/repo/config.json` (기본 `~/.config/repo/config.json`) |
| 클론 이력 | `$XDG_STATE_HOME/repo/history.json` (기본 `~/.local/state/repo/history.json`) |
| 갱신 락·마커·로그 | `$XDG_STATE_HOME/repo/` (기본 `~/.local/state/repo/`) |

클론 이력은 성공한 clone만 기록하고, 180일보다 오래된 항목과 500건 초과분은 쓰기 시점에 정리합니다.
지워도 정렬 기준만 초기화되고 나머지 동작에는 영향이 없습니다.

동작 방식:

- 모든 명령은 캐시를 읽어 즉시 동작하고, 갱신은 분리된(`setsid`) 자식 프로세스가 담당합니다. 터미널을 붙잡지 않습니다.
- 동시 실행 시 중복 요청을 막기 위해 `refresh.lock`에 **flock**을 잡고, 실패하면 조용히 넘어갑니다.
- 캐시 파일은 임시 파일에 쓰고 `rename`하므로(원자적 교체, 권한 0600) 읽는 쪽이 깨진 파일을 보지 않습니다.
- 갱신 로그는 `refresh.log`에 남습니다. `repo cache info`가 마지막 몇 줄을 보여줍니다.
- 캐시가 비어 있는 첫 실행만 포그라운드로 가져옵니다.
- 목록은 `/user/repos`를 100개씩 페이지로 가져오며, 첫 페이지의 `Link` 헤더로 전체 페이지 수를 알아낸 뒤 나머지를 병렬로 받습니다(66개 기준 약 1초).

## 설정

`repo config init`으로 기본값 파일을 만들고 필요한 항목만 바꾸면 됩니다.

```json
{
  "host": "github.com",
  "protocol": "https",
  "affiliation": "owner,collaborator,organization_member",
  "clone_dir": "",
  "include_forks": true,
  "include_archived": true,
  "refresh_min_interval_seconds": 0,
  "clone_concurrency": 2,
  "git_args": []
}
```

- `clone_dir`: 비어 있으면 현재 디렉터리에 clone합니다. `~` 확장을 지원합니다.
- `refresh_min_interval_seconds`: `0`이면 실행마다 갱신합니다. 예: `300`이면 캐시가 5분보다 최신일 때 갱신을 건너뜁니다.
- `clone_concurrency`: 큐에서 동시에 실행할 clone 개수 (`-j`로 덮어쓰기).
- `git_args`: 모든 clone에 붙는 인자 (예: `["--filter=blob:none"]`).
- `host`에 GitHub Enterprise 호스트를 넣으면 `https://<host>/api/v3`로 붙습니다.

환경변수로 덮어쓸 수 있습니다: `REPO_HOST`, `REPO_PROTOCOL`, `REPO_AFFILIATION`, `REPO_CLONE_DIR`,
`REPO_CLONE_CONCURRENCY`, `REPO_REFRESH_MIN_INTERVAL`, `REPO_CONFIG_FILE`, `REPO_CACHE_FILE`, `REPO_HISTORY_FILE`,
`REPO_NO_REFRESH=1`(갱신 끄기, `--no-refresh`와 동일), `REPO_DEBUG=1`(백그라운드 갱신·이력 기록 실패를 stderr에 표시).

## 종료 코드

| 코드 | 의미 |
| --- | --- |
| 0 | 성공 |
| 1 | 오류 (토큰 없음, 매치 없음, clone 실패 등) |
| 130 | 아무것도 큐에 넣지 않고 피커를 닫음 |

## 개발

```bash
make test       # go test ./...
make lint       # go vet + gofmt -l
make build      # ./bin/repo
make dist       # 플랫폼별 릴리스 아카이브 + checksums.txt → ./dist
```

릴리스는 태그를 밀면 끝납니다. `v*` 태그가 올라오면 `.github/workflows/release.yml`이
`make dist`로 크로스 컴파일하고 아카이브(`repo_<버전>_<os>_<arch>.tar.gz`)와 `checksums.txt`를
GitHub 릴리스로 올립니다.

구조:

```
main.go
internal/
  cli/        cobra 명령 정의, 자동완성, 큐 리포트
  tui/        bubbletea 퍼지 피커 + 클론 큐 패널
  cloner/     클론 큐 (워커 풀, 중복·경로 충돌 방지)
  fuzzy/      fzf 스타일 매칭 (affine-gap DP)
  query/      필터 · 랭킹 · 모호성 판정
  history/    클론 이력 기록 · 최근 클론 횟수 집계
  ghapi/      GitHub REST 클라이언트, 토큰 탐색
  cache/      XDG 캐시 원자적 읽기/쓰기
  refresh/    백그라운드 갱신, flock
  config/     설정 파일
  xdg/        XDG 경로
```

## 라이센스

[Apache License 2.0](LICENSE) © 2026 Il Sub Bang
