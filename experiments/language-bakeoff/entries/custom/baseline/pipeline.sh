#!/bin/sh
# Direct-tools baseline: the same workload as examples/pipeline.wb as a
# plain POSIX script. It performs the same underlying operations (write a
# text file, run tr and awk over it) and keeps its own small records: one
# stamp per step with the checksums of the input it read, the command, and
# the output it left. A step reruns when its stamp no longer matches.
#
# usage: baseline/pipeline.sh <workspace>
# The desired input text is NOTES_TEXT; changing it is the baseline's
# equivalent of editing the .wb source.
set -eu

ws=${1:?usage: pipeline.sh <workspace>}
text=${NOTES_TEXT:-"the quick brown fox jumps over the lazy dog"}
cd "$ws"
mkdir -p .stamps

sum() { if [ -f "$1" ]; then cksum < "$1"; else echo absent; fi; }

# The input artifact: rewrite it only when its content differs.
printf '%s\n' "$text" > .stamps/notes.want
if cmp -s .stamps/notes.want notes.txt; then
	rm .stamps/notes.want
	echo "notes: unchanged"
else
	mv .stamps/notes.want notes.txt
	echo "notes: wrote notes.txt"
fi

# step NAME OUTPUT CMD...: run CMD with notes.txt on stdin unless the input,
# the command and the output all match the last successful run.
step() {
	name=$1 out=$2
	shift 2
	if [ -f ".stamps/$name" ] && [ "$(cat ".stamps/$name")" = "$(sum notes.txt) $* $(sum "$out")" ]; then
		echo "$name: current"
		return
	fi
	"$@" < notes.txt > "$out.tmp"
	mv "$out.tmp" "$out"
	echo "$(sum notes.txt) $* $(sum "$out")" > ".stamps/$name"
	echo "$name: ran"
}

step shout notes.upper.txt tr a-z A-Z
step count notes.count.txt awk '{ words += NF } END { print words }'
