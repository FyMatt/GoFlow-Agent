# plan-implement-audit Workflow

This workflow is executable with:

```text
/workflow plan-implement-audit <request>
```

Edit `workflow.yaml` to declare staged agent/skill orchestration.

## Stages

1. Plan: collect context and produce executable steps.
2. Implement: apply approved changes with the fixer agent.
3. Audit: verify results and report residual risk.

## Approval Boundaries

- `approval: true` pauses before a stage starts.
- Tool approval is separate. A stage can start, then pause again if the selected agent calls a confirm-policy tool such as `file_tools/write_file`.
- After tool approval, GoFlow resumes the suspended stage with the approved tool result and then continues to the next stage.

For dynamic branching, have the planning stage produce one of:

- Next skill: code-writing
- Next skills: code-writing, code-audit

## Implementation Notes

- Keep each stage tied to one agent permission boundary.
- Use skill names to keep stage prompts reusable.
- Add explicit approval boundaries before write or exec stages.
- See `docs/workflows.md` for branch selection, stage approval, and approval-resume examples.
