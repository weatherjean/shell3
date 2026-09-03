(task "e2e"
  (root ".")
  (parallel 1)

  (loop implement
    (using builder)
    (access write)
    (max 3)

    (prompt """
Perform the next fixture increment.
""")

    (until
      (sh "test -f \"$TASK_ARTIFACTS/done\""))))
