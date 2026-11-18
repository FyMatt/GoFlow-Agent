#!/usr/bin/env python3
"""Deterministic Office/WPS template fixture for the P7 document benchmark."""

from __future__ import annotations

import csv
import hashlib
import json
import shutil
import zipfile
from io import StringIO
from pathlib import Path


FIXED_DECLARATION = "固定声明：本报告仅用于已授权环境，未经书面批准不得扩大范围。"
MASTER_DECLARATION = "固定母版声明：内部授权交付材料"


def _write_zip(path: Path, entries: dict[str, str]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for name in sorted(entries):
            archive.writestr(name, entries[name].encode("utf-8"))


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _build_docx(path: Path) -> None:
    entries = {
        "[Content_Types].xml": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
 <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
 <Default Extension="xml" ContentType="application/xml"/>
 <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
 <Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
 <Override PartName="/word/settings.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"/>
 <Override PartName="/word/header1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/>
 <Override PartName="/word/footer1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"/>
 <Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
 <Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>
</Types>''',
        "_rels/.rels": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
 <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
 <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>
 <Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>
</Relationships>''',
        "docProps/core.xml": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:title>授权安全评估报告模板</dc:title><dc:creator>GoFlow P7</dc:creator></cp:coreProperties>''',
        "docProps/app.xml": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"><Application>GoFlow</Application><Pages>3</Pages></Properties>''',
        "word/_rels/document.xml.rels": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
 <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
 <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/settings" Target="settings.xml"/>
 <Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/>
 <Relationship Id="rId4" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/>
</Relationships>''',
        "word/styles.xml": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
 <w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="正文"/><w:rPr><w:rFonts w:ascii="Arial" w:eastAsia="微软雅黑"/><w:sz w:val="21"/></w:rPr><w:pPr><w:spacing w:after="120" w:line="360" w:lineRule="auto"/></w:pPr></w:style>
 <w:style w:type="paragraph" w:styleId="Title"><w:name w:val="标题"/><w:basedOn w:val="Normal"/><w:rPr><w:b/><w:color w:val="202124"/><w:sz w:val="36"/></w:rPr><w:pPr><w:jc w:val="center"/><w:spacing w:after="300"/></w:pPr></w:style>
 <w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="标题 1"/><w:basedOn w:val="Normal"/><w:qFormat/><w:rPr><w:b/><w:color w:val="1A73E8"/><w:sz w:val="28"/></w:rPr><w:pPr><w:outlineLvl w:val="0"/><w:spacing w:before="260" w:after="120"/></w:pPr></w:style>
 <w:style w:type="table" w:styleId="AuditTable"><w:name w:val="审计表格"/><w:tblPr><w:tblBorders><w:top w:val="single" w:sz="4" w:color="C8CDD4"/><w:left w:val="single" w:sz="4" w:color="C8CDD4"/><w:bottom w:val="single" w:sz="4" w:color="C8CDD4"/><w:right w:val="single" w:sz="4" w:color="C8CDD4"/><w:insideH w:val="single" w:sz="4" w:color="DADCE0"/><w:insideV w:val="single" w:sz="4" w:color="DADCE0"/></w:tblBorders></w:tblPr></w:style>
</w:styles>''',
        "word/settings.xml": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:settings xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:updateFields w:val="true"/></w:settings>''',
        "word/header1.xml": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:pPr><w:jc w:val="right"/></w:pPr><w:r><w:rPr><w:color w:val="5F6368"/><w:sz w:val="18"/></w:rPr><w:t>GoFlow · 授权安全评估</w:t></w:r></w:p></w:hdr>''',
        "word/footer1.xml": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:pPr><w:jc w:val="center"/></w:pPr><w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText xml:space="preserve"> PAGE </w:instrText></w:r><w:r><w:fldChar w:fldCharType="end"/></w:r></w:p></w:ftr>''',
        "word/document.xml": f'''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body>
 <w:p><w:pPr><w:pStyle w:val="Title"/></w:pPr><w:r><w:t>{{{{REPORT_TITLE}}}}</w:t></w:r></w:p>
 <w:p><w:pPr><w:jc w:val="center"/></w:pPr><w:r><w:t>客户：{{{{CLIENT_NAME}}}} · 日期：{{{{REPORT_DATE}}}}</w:t></w:r></w:p>
 <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>目录</w:t></w:r></w:p>
 <w:p><w:fldSimple w:instr=" TOC \\o &quot;1-3&quot; \\h \\z \\u "><w:r><w:t>打开文档后更新目录</w:t></w:r></w:fldSimple></w:p>
 <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>执行摘要</w:t></w:r></w:p>
 <w:p><w:r><w:t>{{{{EXECUTIVE_SUMMARY}}}}</w:t></w:r></w:p>
 <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>范围与方法</w:t></w:r></w:p>
 <w:p><w:r><w:t>本报告仅覆盖已授权的本地评估范围。每项结论必须能够追溯到输入发现、整改动作和独立复测记录；未取得证据的事项不判定为完成，也不扩大到未授权系统。</w:t></w:r></w:p>
 <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>发现与整改</w:t></w:r></w:p>
 <w:tbl><w:tblPr><w:tblStyle w:val="AuditTable"/><w:tblW w:w="9360" w:type="dxa"/><w:tblLayout w:type="fixed"/></w:tblPr><w:tblGrid><w:gridCol w:w="1300"/><w:gridCol w:w="1500"/><w:gridCol w:w="3280"/><w:gridCol w:w="3280"/></w:tblGrid>
  <w:tr><w:tc><w:p><w:r><w:t>编号</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>等级</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>证据</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>整改</w:t></w:r></w:p></w:tc></w:tr>
  <w:tr><w:tc><w:p><w:r><w:t>{{{{FINDING_1_ID}}}}</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>{{{{FINDING_1_SEVERITY}}}}</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>{{{{FINDING_1_EVIDENCE}}}}</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>{{{{FINDING_1_RECOMMENDATION}}}}</w:t></w:r></w:p></w:tc></w:tr>
  <w:tr><w:tc><w:p><w:r><w:t>{{{{FINDING_2_ID}}}}</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>{{{{FINDING_2_SEVERITY}}}}</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>{{{{FINDING_2_EVIDENCE}}}}</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>{{{{FINDING_2_RECOMMENDATION}}}}</w:t></w:r></w:p></w:tc></w:tr>
 </w:tbl>
 <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>复测与质量结论</w:t></w:r></w:p>
 <w:p><w:r><w:t>{{{{REMEDIATION_SUMMARY}}}} 复测必须覆盖授权默认拒绝、敏感信息不明文输出以及原有正常路径不回归。结构校验、内容核对和预览检查全部通过后，方可将文件列为可交付。</w:t></w:r></w:p>
 <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>剩余风险与后续行动</w:t></w:r></w:p>
 <w:p><w:r><w:t>{{{{NEXT_ACTIONS}}}} 后续新增角色、操作或敏感字段时，应同步更新授权矩阵、脱敏规则和回归测试；任何超出本报告范围的验证必须重新取得书面授权。</w:t></w:r></w:p>
 <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>回滚与证据保留</w:t></w:r></w:p>
 <w:p><w:r><w:t>发布前保留原文件哈希和备份。若复测失败，停止交付并恢复变更前版本；保留失败日志、修正记录、最终文件哈希和独立校验结果，确保后续人员可以复核。</w:t></w:r></w:p>
 <w:p><w:r><w:rPr><w:b/></w:rPr><w:t>{FIXED_DECLARATION}</w:t></w:r></w:p>
 <w:sectPr><w:headerReference w:type="default" r:id="rId3"/><w:footerReference w:type="default" r:id="rId4"/><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="1080" w:right="1440" w:bottom="1080" w:left="1440" w:header="720" w:footer="720"/></w:sectPr>
</w:body></w:document>''',
    }
    _write_zip(path, entries)


def _cell_inline(ref: str, value: str) -> str:
    return f'<c r="{ref}" t="inlineStr"><is><t>{value}</t></is></c>'


def _build_xlsx(path: Path) -> None:
    rows = ['<row r="1">' + ''.join([
        _cell_inline("A1", "发现编号"), _cell_inline("B1", "等级"),
        _cell_inline("C1", "证据"), _cell_inline("D1", "整改建议"),
        _cell_inline("E1", "状态"),
    ]) + '</row>']
    for index in range(1, 3):
        row = index + 1
        rows.append(
            f'<row r="{row}">{_cell_inline(f"A{row}", f"{{{{FINDING_{index}_ID}}}}")}'
            f'{_cell_inline(f"B{row}", f"{{{{FINDING_{index}_SEVERITY}}}}")}'
            f'{_cell_inline(f"C{row}", f"{{{{FINDING_{index}_EVIDENCE}}}}")}'
            f'{_cell_inline(f"D{row}", f"{{{{FINDING_{index}_RECOMMENDATION}}}}")}'
            f'{_cell_inline(f"E{row}", f"{{{{FINDING_{index}_STATUS}}}}")}</row>'
        )
    data_sheet = f'''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews><cols><col min="1" max="1" width="16" customWidth="1"/><col min="2" max="2" width="12" customWidth="1"/><col min="3" max="4" width="42" customWidth="1"/><col min="5" max="5" width="14" customWidth="1"/></cols><sheetData>{''.join(rows)}</sheetData><autoFilter ref="A1:E3"/><printOptions horizontalCentered="1"/><pageMargins left="0.3" right="0.3" top="0.5" bottom="0.5" header="0.2" footer="0.2"/><pageSetup orientation="landscape" fitToWidth="1" fitToHeight="1"/></worksheet>'''
    summary_sheet = '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews><cols><col min="1" max="1" width="28" customWidth="1"/><col min="2" max="2" width="18" customWidth="1"/></cols><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>{{CLIENT_NAME}} - 整改汇总</t></is></c><c r="B1" t="inlineStr"><is><t>数值</t></is></c></row><row r="2"><c r="A2" t="inlineStr"><is><t>发现总数</t></is></c><c r="B2"><f>COUNTA('明细数据'!A2:A3)</f><v>2</v></c></row><row r="3"><c r="A3" t="inlineStr"><is><t>高风险数量</t></is></c><c r="B3"><f>COUNTIF('明细数据'!B2:B3,&quot;高&quot;)</f><v>1</v></c></row><row r="4"><c r="A4" t="inlineStr"><is><t>中风险数量</t></is></c><c r="B4"><f>COUNTIF('明细数据'!B2:B3,&quot;中&quot;)</f><v>1</v></c></row><row r="5"><c r="A5" t="inlineStr"><is><t>风险分类合计</t></is></c><c r="B5"><f>SUM(B3:B4)</f><v>2</v></c></row><row r="6"><c r="A6" t="inlineStr"><is><t>整改完成率</t></is></c><c r="B6" s="1"><f>IFERROR(COUNTIF('明细数据'!E2:E3,&quot;已完成&quot;)/B2,0)</f><v>1</v></c></row></sheetData><printOptions horizontalCentered="1"/><pageMargins left="0.5" right="0.5" top="0.6" bottom="0.6" header="0.2" footer="0.2"/><pageSetup orientation="portrait" fitToWidth="1" fitToHeight="1"/></worksheet>'''
    entries = {
        "[Content_Types].xml": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/worksheets/sheet2.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/></Types>''',
        "_rels/.rels": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>''',
        "xl/workbook.xml": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="明细数据" sheetId="1" r:id="rId1"/><sheet name="统计汇总" sheetId="2" r:id="rId2"/></sheets><calcPr calcId="191029" fullCalcOnLoad="1" forceFullCalc="1"/></workbook>''',
        "xl/_rels/workbook.xml.rels": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>''',
        "xl/styles.xml": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><numFmts count="1"><numFmt numFmtId="164" formatCode="0.0%"/></numFmts><fonts count="1"><font><sz val="11"/><name val="Microsoft YaHei"/></font></fonts><fills count="1"><fill><patternFill patternType="none"/></fill></fills><borders count="1"><border/></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs><cellXfs count="2"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="164" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/></cellXfs></styleSheet>''',
        "xl/worksheets/sheet1.xml": data_sheet,
        "xl/worksheets/sheet2.xml": summary_sheet,
    }
    _write_zip(path, entries)


def _slide_xml(title: str, body: str) -> str:
    return f'''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/><p:sp><p:nvSpPr><p:cNvPr id="2" name="标题"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="685800" y="514350"/><a:ext cx="10820400" cy="914400"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln><a:noFill/></a:ln></p:spPr><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="zh-CN" sz="3200" b="1"/><a:t>{title}</a:t></a:r><a:endParaRPr lang="zh-CN"/></a:p></p:txBody></p:sp><p:sp><p:nvSpPr><p:cNvPr id="3" name="正文"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="685800" y="1714500"/><a:ext cx="10820400" cy="4000500"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln><a:noFill/></a:ln></p:spPr><p:txBody><a:bodyPr wrap="square"/><a:lstStyle/><a:p><a:r><a:rPr lang="zh-CN" sz="2000"/><a:t>{body}</a:t></a:r><a:endParaRPr lang="zh-CN"/></a:p></p:txBody></p:sp></p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>'''


def _build_pptx(path: Path) -> None:
    verified_template = Path(__file__).resolve().parent / "assets" / "p7-review-template.pptx"
    if not verified_template.is_file():
        raise RuntimeError(f"verified PPTX template is missing: {verified_template}")
    path.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(verified_template, path)
    return

    entries = {
        "[Content_Types].xml": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/><Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/><Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/><Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/><Override PartName="/ppt/slides/slide1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/><Override PartName="/ppt/slides/slide2.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/><Override PartName="/ppt/slides/slide3.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/></Types>''',
        "_rels/.rels": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/></Relationships>''',
        "ppt/presentation.xml": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst><p:sldIdLst><p:sldId id="256" r:id="rId2"/><p:sldId id="257" r:id="rId3"/><p:sldId id="258" r:id="rId4"/></p:sldIdLst><p:sldSz cx="12192000" cy="6858000" type="screen16x9"/><p:notesSz cx="6858000" cy="9144000"/></p:presentation>''',
        "ppt/_rels/presentation.xml.rels": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide1.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide2.xml"/><Relationship Id="rId4" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide3.xml"/></Relationships>''',
        "ppt/slideMasters/slideMaster1.xml": f'''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldMaster xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/><p:sp><p:nvSpPr><p:cNvPr id="2" name="固定声明"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="685800" y="6350000"/><a:ext cx="10820400" cy="250000"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln><a:noFill/></a:ln></p:spPr><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr sz="900"/><a:t>{MASTER_DECLARATION}</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld><p:clrMap accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" bg1="lt1" bg2="lt2" folHlink="folHlink" hlink="hlink" tx1="dk1" tx2="dk2"/><p:sldLayoutIdLst><p:sldLayoutId id="1" r:id="rId1"/></p:sldLayoutIdLst><p:txStyles><p:titleStyle/><p:bodyStyle/><p:otherStyle/></p:txStyles></p:sldMaster>''',
        "ppt/slideMasters/_rels/slideMaster1.xml.rels": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="../theme/theme1.xml"/></Relationships>''',
        "ppt/slideLayouts/slideLayout1.xml": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldLayout xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" type="blank" preserve="1"><p:cSld name="空白"><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/></p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>''',
        "ppt/slideLayouts/_rels/slideLayout1.xml.rels": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="../slideMasters/slideMaster1.xml"/></Relationships>''',
        "ppt/theme/theme1.xml": '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="GoFlow 简约主题"><a:themeElements><a:clrScheme name="GoFlow"><a:dk1><a:srgbClr val="202124"/></a:dk1><a:lt1><a:srgbClr val="FFFFFF"/></a:lt1><a:dk2><a:srgbClr val="3C4043"/></a:dk2><a:lt2><a:srgbClr val="F8F9FA"/></a:lt2><a:accent1><a:srgbClr val="1A73E8"/></a:accent1><a:accent2><a:srgbClr val="34A853"/></a:accent2><a:accent3><a:srgbClr val="FBBC04"/></a:accent3><a:accent4><a:srgbClr val="EA4335"/></a:accent4><a:accent5><a:srgbClr val="5F6368"/></a:accent5><a:accent6><a:srgbClr val="9AA0A6"/></a:accent6><a:hlink><a:srgbClr val="1155CC"/></a:hlink><a:folHlink><a:srgbClr val="681DA8"/></a:folHlink></a:clrScheme><a:fontScheme name="GoFlow"><a:majorFont><a:latin typeface="Arial"/><a:ea typeface="Microsoft YaHei"/><a:cs typeface="Arial"/></a:majorFont><a:minorFont><a:latin typeface="Arial"/><a:ea typeface="Microsoft YaHei"/><a:cs typeface="Arial"/></a:minorFont></a:fontScheme><a:fmtScheme name="GoFlow"><a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst><a:lnStyleLst><a:ln w="12700"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln></a:lnStyleLst><a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst><a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst></a:fmtScheme></a:themeElements></a:theme>''',
        "ppt/slides/slide1.xml": _slide_xml("{{PRESENTATION_TITLE}}", "{{PRESENTATION_SUBTITLE}}"),
        "ppt/slides/slide2.xml": _slide_xml("整改结果", "{{REMEDIATION_SUMMARY}}"),
        "ppt/slides/slide3.xml": _slide_xml("后续行动", "{{NEXT_ACTIONS}}"),
    }
    for index in range(1, 4):
        entries[f"ppt/slides/_rels/slide{index}.xml.rels"] = '''<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/></Relationships>'''
    _write_zip(path, entries)


def build_documents_fixture(root: Path) -> None:
    templates = root / "templates"
    _build_docx(templates / "security-report-template.docx")
    _build_xlsx(templates / "remediation-data-template.xlsx")
    _build_pptx(templates / "review-template.pptx")

    csv_buffer = StringIO()
    writer = csv.writer(csv_buffer, lineterminator="\n")
    writer.writerow(["finding_id", "severity", "evidence", "recommendation"])
    writer.writerow(["SEC-001", "高", "授权检查默认允许未知角色", "未知角色改为默认拒绝并补充回归测试"])
    writer.writerow(["SEC-002", "中", "审计日志记录完整令牌", "仅保留令牌哈希前八位"])
    (root / "inputs").mkdir(parents=True, exist_ok=True)
    (root / "inputs" / "findings.csv").write_text(csv_buffer.getvalue(), encoding="utf-8", newline="\n")

    request = {
        "client": "星河科技",
        "report_title": "Alpha 平台安全整改交付报告",
        "report_date": "2026-08-18",
        "executive_summary": "已完成两项有证据支持的整改，并通过公开与隐藏复测。",
        "presentation_subtitle": "授权范围内的整改、复测与后续行动",
        "required_outputs": ["report.docx", "data.xlsx", "review.pptx", "report.pdf"],
        "fixed_content": [FIXED_DECLARATION, MASTER_DECLARATION],
    }
    (root / "inputs" / "request.json").write_text(json.dumps(request, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")
    (root / "inputs" / "merge-map.json").write_text(
        json.dumps(
            {
                "docx": {
                    "REPORT_TITLE": request["report_title"], "CLIENT_NAME": request["client"],
                    "REPORT_DATE": request["report_date"], "EXECUTIVE_SUMMARY": request["executive_summary"],
                    "FINDING_1_ID": "SEC-001", "FINDING_1_SEVERITY": "高",
                    "FINDING_1_EVIDENCE": "授权检查默认允许未知角色",
                    "FINDING_1_RECOMMENDATION": "未知角色改为默认拒绝并补充回归测试",
                    "FINDING_2_ID": "SEC-002", "FINDING_2_SEVERITY": "中",
                    "FINDING_2_EVIDENCE": "审计日志记录完整令牌",
                    "FINDING_2_RECOMMENDATION": "仅保留令牌哈希前八位",
                    "REMEDIATION_SUMMARY": "2 项整改已完成，独立复测通过。",
                    "NEXT_ACTIONS": "保持变更记录，按周期复核授权边界。",
                },
                "pptx": {
                    "PRESENTATION_TITLE": request["report_title"],
                    "PRESENTATION_SUBTITLE": request["presentation_subtitle"],
                    "REMEDIATION_SUMMARY": "2 项整改已完成，独立复测通过。",
                    "NEXT_ACTIONS": "保持变更记录，按周期复核授权边界。",
                },
                "xlsx": {
                    "source": "inputs/findings.csv", "target_sheet": "明细数据", "CLIENT_NAME": request["client"],
                    "FINDING_1_ID": "SEC-001", "FINDING_1_SEVERITY": "高",
                    "FINDING_1_EVIDENCE": "授权检查默认允许未知角色",
                    "FINDING_1_RECOMMENDATION": "未知角色改为默认拒绝并补充回归测试",
                    "FINDING_1_STATUS": "已完成", "FINDING_2_ID": "SEC-002", "FINDING_2_SEVERITY": "中",
                    "FINDING_2_EVIDENCE": "审计日志记录完整令牌",
                    "FINDING_2_RECOMMENDATION": "仅保留令牌哈希前八位", "FINDING_2_STATUS": "已完成",
                },
            },
            ensure_ascii=False,
            indent=2,
        ) + "\n",
        encoding="utf-8",
        newline="\n",
    )
    manifest = {
        "schema_version": 1,
        "templates": {
            path.name: {"sha256": _sha256(path), "bytes": path.stat().st_size}
            for path in sorted(templates.iterdir())
        },
        "required_preservation": [
            "DOCX 页眉、页脚、目录域、表格样式和固定声明",
            "XLSX 公式、筛选、冻结首行和打印设置",
            "PPTX 16:9 页面、幻灯片母版、主题和固定母版声明",
        ],
        "atomicity_probe": {
            "target": "templates/security-report-template.docx",
            "valid_old_text": "{{CLIENT_NAME}}",
            "missing_old_text": "{{INTENTIONALLY_MISSING_TOKEN}}",
            "expected": "整批失败且源模板 SHA-256 不变",
        },
    }
    (root / "fixture-manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")
    (root / "README.md").write_text(
        "# Office/WPS 组合交付夹具\n\n输入包含真实 OOXML 模板、结构化数据、固定声明和原子失败探针。\n",
        encoding="utf-8", newline="\n",
    )


DOCUMENTS_HIDDEN = {
    "acceptance/hidden-documents.json": json.dumps(
        {
            "required_replacements": ["星河科技", "Alpha 平台安全整改交付报告", "2026-08-18", "SEC-001", "SEC-002"],
            "required_preserved": [FIXED_DECLARATION, MASTER_DECLARATION],
            "require_docx_toc": True,
            "require_xlsx_formulas": True,
            "require_xlsx_auto_filter": True,
            "require_xlsx_frozen_rows": True,
            "require_xlsx_print_setup": True,
            "require_pptx_master": True,
            "require_pdf_and_preview": True,
            "require_atomic_failure_evidence": True,
        },
        ensure_ascii=False,
        indent=2,
    ) + "\n",
}
