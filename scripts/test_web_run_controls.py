import json
import re
import subprocess
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


class WebRunControlTests(unittest.TestCase):
    def test_quality_diagnostics_and_cache_labels_are_chinese(self) -> None:
        script = """
          import {readFileSync} from 'node:fs';
          globalThis.localStorage = {getItem:()=>null};
          const source=readFileSync('internal/api/web/assets/i18n.js','utf8');
          const i18n=await import('data:text/javascript;base64,'+Buffer.from(source).toString('base64'));
          console.log(JSON.stringify({
            title:i18n.localizedText('Revision summary'),
            control:i18n.localizedText('quality_gate control route=fail target=revision-summary'),
            slice:i18n.localizedText('stage verify failed before this slice was verified'),
            diagnostic:i18n.localizedText('final report has 12 unresolved quality issue(s): 10 failed, 2 warning - implement/response: response failed'),
            sliceContract:i18n.localizedText('workflow stage plan: workflow stage plan produced no machine-checkable change slices: 切片 mode 无效，只允许 read 或 write'),
            providerWait:i18n.localizedText('process stopped while waiting for provider response; continue the saved stage without replaying completed stages'),
            cached:i18n.t('chat.tokenCached'),
            hit:i18n.t('chat.tokenCacheHit')
          }));
        """
        result = subprocess.run(["node", "--input-type=module", "--eval", script], cwd=ROOT, text=True, encoding="utf-8", capture_output=True, check=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        labels = json.loads(result.stdout)
        self.assertEqual(labels["title"], "修订说明")
        self.assertIn("12 项质量问题", labels["diagnostic"])
        self.assertNotIn("unresolved", labels["diagnostic"])
        self.assertIn("变更切片未通过校验", labels["sliceContract"])
        self.assertIn("read 或 write", labels["sliceContract"])
        self.assertNotIn("workflow stage", labels["sliceContract"])
        self.assertIn("已完成阶段不会重新执行", labels["providerWait"])
        self.assertEqual(labels["control"], "质量门禁：失败；后续阶段：修订说明")
        self.assertNotIn("this slice", labels["slice"])
        self.assertEqual([labels["cached"], labels["hit"]], ["缓存读取", "缓存命中率"])

    def test_quality_blocking_is_not_a_runtime_error_and_recovery_wins(self) -> None:
        source = (ROOT / "internal/api/web/assets/views/chat.js").read_text(encoding="utf-8")
        helpers = []
        for name in ("workflowRunEffectiveStatus", "workflowRunTimelineLabel", "workflowRunResultLabel"):
            match = re.search(rf"^(?:export )?function {name}\([^\n]*\) \{{\n.*?^\}}", source, re.M | re.S)
            self.assertIsNotNone(match, name)
            helpers.append(match.group(0).removeprefix("export "))
        script = "\n".join(helpers) + """
          const t = key => key;
          const failed = {node_type:'quality_gate',outputs:{quality_status:'failed'}};
          const passed = {node_type:'quality_gate',output_values:{quality_status:'passed'}};
          const run = {status:'completed',completed_stages:[{outputs:{decision:'failed'}},failed]};
          const blocked = workflowRunEffectiveStatus(run);
          run.completed_stages.push(passed);
          console.log(JSON.stringify([blocked, workflowRunEffectiveStatus(run),workflowRunTimelineLabel(blocked),workflowRunResultLabel(blocked,true)]));
        """
        result = subprocess.run(["node", "--eval", script], text=True, encoding="utf-8", capture_output=True, check=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout), ["blocked", "completed", "chat.timelineBlocked", "chat.resultQualityBlocked"])

    def test_snapshot_replay_does_not_add_usage_again(self) -> None:
        source = (ROOT / "internal/api/web/assets/views/chat.js").read_text(encoding="utf-8")
        helpers = []
        for name in ("appendSnapshotTimelineEvent", "agentRunLatestEventSeq", "workflowRunLatestEventSeq", "workflowRunEventSeq"):
            match = re.search(rf"^function {name}\([^\n]*\) \{{\n.*?^\}}", source, re.M | re.S)
            self.assertIsNotNone(match, name)
            helpers.append(match.group(0))
        script = "\n".join(helpers) + """
          const queued = [];
          const appendTimelineEvent = (_, event) => queued.push(event);
          appendSnapshotTimelineEvent({}, {accountUsage:true,usage:{input:100}});
          const run = {events:[],timeline:[{seq:41},{seq:42}]};
          console.log(JSON.stringify({accountUsage:queued[0].accountUsage,agent:agentRunLatestEventSeq(run),workflow:workflowRunLatestEventSeq(run)}));
        """
        result = subprocess.run(["node", "--eval", script],text=True,encoding="utf-8",capture_output=True,check=False)
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(json.loads(result.stdout),{"accountUsage":False,"agent":42,"workflow":42})

    def test_reported_totals_replace_inflated_stage_sums(self) -> None:
        source = (ROOT / "internal/api/web/assets/views/chat.js").read_text(encoding="utf-8")
        match = re.search(r"^function syncTimelineTotalsFromRun\([^\n]*\) \{\n.*?^\}", source, re.M | re.S)
        self.assertIsNotNone(match)
        script = match.group(0) + """
          const runTokenUsageSummary = () => ({reported:true,prompt:1500,output:250,cached:600,total:1750,samples:3});
          const renderTimelineRunTotals = () => {};
          const state = {tokenTotals:{prompt:90000,input:90000,output:9000,cached:8000,total:99000,samples:50}};
          syncTimelineTotalsFromRun(state, {});
          console.log(JSON.stringify(state.tokenTotals));
        """
        result = subprocess.run(["node", "--eval", script], text=True, encoding="utf-8", capture_output=True, check=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        totals = json.loads(result.stdout)
        self.assertEqual([totals[key] for key in ("input", "output", "cached", "total", "samples")], [1500, 250, 600, 1750, 3])
        self.assertEqual(totals["cacheHitRate"], 0.4)

    def test_reasoning_preferences_follow_model_and_configured_default(self) -> None:
        source = (ROOT / "internal/api/web/assets/views/chat.js").read_text(encoding="utf-8")
        helpers = []
        for name in ("runtimeReasoningEffort", "reasoningEffortKey", "storedRunControl"):
            match = re.search(rf"^function {name}\([^\n]*\) \{{\n.*?^\}}", source, re.M | re.S)
            self.assertIsNotNone(match, name)
            helpers.append(match.group(0))
        script = "\n".join(helpers) + """
          const reasoningEffortStorageKey = 'goflow.playground.reasoningEffort';
          const values = new Map([[reasoningEffortStorageKey, 'mid']]);
          const localStorage = { getItem: key => values.get(key) };
          const runtime = {
            active_agent: 'engineer',
            agents: [{ id: 'engineer', provider: 'primary' }],
            providers: [{ id: 'primary', model: 'gpt-6.1-sol', reasoning_effort: 'low' }]
          };
          const read = () => storedRunControl(reasoningEffortKey(runtime), runtimeReasoningEffort(runtime));
          const result = [read()];
          values.set(reasoningEffortKey(runtime), 'high');
          result.push(read());
          runtime.providers[0].model = 'other-gateway-model';
          result.push(read());
          runtime.providers[0].model = 'gpt-6.1-sol';
          result.push(read());
          runtime.providers[0].reasoning_effort = 'medium';
          result.push(read());
          runtime.agents[0].model = 'agent-model-override';
          result.push(read());
          console.log(JSON.stringify(result));
        """
        completed = subprocess.run(
            ["node", "--input-type=module", "--eval", script], cwd=ROOT,
            text=True, encoding="utf-8", errors="replace", capture_output=True, check=False,
        )
        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertEqual(json.loads(completed.stdout), ["low", "high", "low", "high", "mid", "mid"])


if __name__ == "__main__":
    unittest.main()
