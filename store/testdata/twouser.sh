#!/bin/sh
# Two users on two machines (containers) share one served folder: alice's change is cut short, bob is refused with unfinished_change naming her, her next run puts it back, then bob's change goes through.
# Both have uid 1000, as each user's own workspace container does, so the queue's private folders (ADR-S14) are theirs alike; only the name and the machine tell them apart.
set -eu
cd "$(dirname "$0")/../.."
image=${TWOUSER_IMAGE:-alpine:3.20}
work=$(mktemp -d)
id=$$
arch=$(docker version -f '{{.Server.Arch}}')
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go test -c -tags twouser -o "$work/store.test" ./store
cleanup() { docker volume rm -f "twouser-shared-$id" "twouser-alice-$id" "twouser-bob-$id" >/dev/null 2>&1 || true; rm -rf "$work"; }
trap cleanup EXIT

# step <user> <machine> <role> [names the refusal must say]
step() {
	echo "== $1 on $2: $3"
	docker run --rm --hostname "$2" -v "twouser-shared-$id:/shared" -v "twouser-$1-$id:/home/$1" -v "$work:/t:ro" \
		-e STORE_TWOUSER_ROLE="$3" -e STORE_TWOUSER_NAMES="${4:-}" "$image" sh -c \
		"adduser -D -u 1000 $1 >/dev/null 2>&1; chown 1000 /shared /home/$1; su $1 -c 'HOME=/home/$1 STORE_TWOUSER_DIR=/shared STORE_TWOUSER_ROLE=\$STORE_TWOUSER_ROLE STORE_TWOUSER_NAMES=\"\$STORE_TWOUSER_NAMES\" /t/store.test -test.run ^TestTwoUsers\$'"
}

step alice host-a seed
code=0
step alice host-a crash || code=$?
[ "$code" -eq 3 ] || { echo "alice's change ended with $code, want 3 (stopped dead)"; exit 1; }
step bob host-b refused "alice host-a"
step alice host-a recover
step bob host-b after
echo "two users: PASS"
