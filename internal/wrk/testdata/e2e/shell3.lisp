(shell3
  (version 1)

  (runner fake
    (command "/bin/sh")
    (arguments "fake-agent.sh" result-file task-artifacts task-attempt)
    (stderr log)
    (result (file result-file))
    (success (exit 0))
    (timeout "5s"))

  (agent builder
    (using fake))

  (schedule acceptance
    (cron "0 0 1 1 *")
    (timezone "UTC")
    (run (wrkfile "demo.wrk.lisp"))
    (request "deterministic scheduled acceptance request")
    (output "done")
    (timeout "30s")
    (overlap skip)
    (notify "main")))
