# Input artifact: persistent desired state. The file adapter owns input.txt
# and every plan compares its live content with this declaration.
resource "file" "input" {
  path    = "input.txt"
  content = <<-EOT
    hello retry
    second line stays
  EOT
}

# Transformation: one-shot work. Referencing file.input.path is the
# dependency edge; exec re-runs only when its receipt goes stale. It owns
# output.txt.
task "exec" "shout" {
  command = ["tr", "a-z", "A-Z"]
  stdin   = file.input.path
  stdout  = "${dir.build.path}/output.txt"
}

# Unrelated resource, declared after the task: if the task fails it still
# applies, and a retry does not rewrite it.
resource "file" "notes" {
  path    = "notes.txt"
  content = "owner: demo\nreviewed: yes\n"
}

# Third adapter: a directory. The task's stdout interpolates dir.build.path;
# that reference orders the directory first. Writing "build/output.txt"
# literally would not compile: the path is inside a block it does not
# reference.
resource "dir" "build" {
  path = "build"
  mode = "0750"
}
