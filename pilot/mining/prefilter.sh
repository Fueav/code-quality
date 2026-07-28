#!/bin/zsh
set -eu

if (( $# != 1 )); then
  print -u2 "usage: $0 <repo_path>"
  exit 2
fi

repo=$1
git -C "$repo" rev-parse --is-inside-work-tree >/dev/null

is_real_source() {
  local file_path=${1:l}

  case "/$file_path/" in
    */docs/*|*/doc/*|*/documentation/*|*/test/*|*/tests/*|*/__tests__/*|*/testdata/*|*/fixtures/*|*/.github/*|*/.gitlab/*|*/ci/*)
      return 1
      ;;
  esac
  case "$file_path" in
    */example/*|*/examples/*|*/sample/*|*/samples/*|*/template/*|*/templates/*|*.example.*|*.sample.*|*.template.*|*.dist.*)
      return 1
      ;;
  esac
  case "$file_path" in
    *.c|*.cc|*.cpp|*.cxx|*.h|*.hh|*.hpp|*.go|*.rs|*.py|*.pyi|*.js|*.jsx|*.mjs|*.cjs|*.ts|*.tsx|*.java|*.kt|*.kts|*.swift|*.rb|*.php|*.scala|*.sh|*.zsh|*.bash|*.sol|*.ex|*.exs|*.erl|*.hrl)
      return 0
      ;;
  esac
  return 1
}

git -C "$repo" log \
  --regexp-ignore-case \
  --extended-regexp \
  --grep='fix|hotfix|revert|bug' \
  --format=$'%H\t%s' |
while IFS=$'\t' read -r sha subject; do
  keep=false
  while IFS= read -r changed_file; do
    if is_real_source "$changed_file"; then
      keep=true
      break
    fi
  done < <(git -C "$repo" diff-tree --root -m --no-commit-id --name-only -r "$sha" | sort -u)

  if [[ "$keep" == true ]]; then
    print -r -- "KEEP $sha :: $subject"
  fi
done
