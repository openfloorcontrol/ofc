# A building: many floors in one web UI

```bash
make fakeagent                  # once, for the fake template (from the repository root)
mf set <model> tags ofc-test    # once, the model of the taskboard and assistant templates
ofc building examples/building  # prints the web UI's URL with its token
```

The start page lists the floors and makes new ones: pick a template, **Add floor**. Each floor is a copy of its template in `floors/<id>/`, with its own sessions, task board and workspace. **Edit** changes a floor's blueprint; it is checked before it is saved.

```
templates/
  fake/        @fake, the agent the tests use: works without a model
  taskboard/   @planner and @coder share a task board (mf model tagged ofc-test)
  assistant/   one assistant whose prompt is in persona.md (mf model tagged ofc-test)
floors/        made from templates by the web UI (not in git)
sessions/      the sessions of all floors (not in git)
```

A template is any directory with a `blueprint.yaml`; add your own beside these. A floor starts when it is opened and stops after `--idle-timeout` (default 30m) without use; its sessions resume when it starts again.
