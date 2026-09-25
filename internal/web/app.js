const { createApp, ref, computed, nextTick } = Vue;

// 指标趋势图：图表可用宽度窄于此值时启用窄屏布局（图例移底、字号 11px、边距收紧）。
// 注意「窄屏」按图表实测宽度判断（弹窗宽度是 min(680px, 92vw)，非固定）；「移动端」按视口宽度，见 style.css。
const CHART_NARROW_WIDTH = 480;

// 语义色统一色值（FR-11）：与 style.css 的 --good / --bad 同值，改色只改此处（CSS 侧同步改令牌）。
// good=向好/健康；goodMid/warn=AI 评分中间档（黄绿/琥珀）；bad=走弱/坏；neutral=图表中性灰；growth=增速折线琥珀。
const PALETTE = {
  good: '#149a5e',
  goodMid: '#6aa84f',
  warn: '#d9a240',
  bad: '#d64040',
  neutral: '#998c8c',
  growth: '#d97706',
};

// ★决策更新 2（FR-16）：tooltip 措辞幅度分级阈值——|R|（%，与 trend_rate 同口径）小于该值时用温和词。
// 与后端 trendThreshold（0.05）**相互独立、不得互相替代**：前者只决定「用词轻重」，后者决定「有无信号」。
// 单点可调（如 15/10），调整后仅改措辞、**不改任何颜色与信号**（AC-27）。
const MILD_CHANGE_THRESHOLD = 20; // %

// FR-10：指标趋势图柱色 —— 向好绿 / 走弱红 / 无信号或中性灰；负值无论信号一律红。
function barColorOf(signal, value) {
  if (value != null && value < 0) return PALETTE.bad;
  if (signal === 'improving') return PALETTE.good;
  if (signal === 'worsening') return PALETTE.bad;
  return PALETTE.neutral;
}

// 百分比刻度：至多 1 位小数（12.5% / 10% / 0% / -5%）；左轴与右轴（同比增速）共用。
function fmtAxisPct(v) {
  if (v === 0) return '0%';
  const r = Math.round(v * 10) / 10;
  return (Number.isInteger(r) ? r.toFixed(0) : r.toFixed(1)) + '%';
}

// 左 y 轴刻度：单位语义由刻度标签承载（FR-1），不再用孤立单位符号作轴名。
function fmtAxisValue(v, unit) {
  if (unit === '元') {
    if (v === 0) return '0';
    const yi = v / 1e8;
    if (Math.abs(yi) >= 1) return yi.toFixed(1) + '亿';
    return (v / 1e4).toFixed(0) + '万';
  }
  if (unit === '%') return fmtAxisPct(v);
  if (unit === '倍') return v.toFixed(2) + '倍';
  return Number(v).toFixed(2);
}

// 窄屏图例缩略：超过 6 个汉字截断为 6 字 + 「…」（完整指标名仍在弹窗标题与 tooltip 中）。
function legendLabel(name, narrow) {
  return narrow && name.length > 6 ? name.slice(0, 6) + '…' : name;
}

// R2 数值格式化：元 → 亿/万（图表 tooltip 与指标表共用）。
function fmtYi(v) {
  if (v == null || isNaN(v)) return '—';
  const yi = v / 1e8;
  if (Math.abs(yi) < 0.01) return (v / 1e4).toFixed(2) + '万';
  return yi.toFixed(2) + '亿';
}

// 同比增速（%）：首年/上期缺失/上期为 0 或负 → null（口径不变）。
function growthSeries(values) {
  return values.map((v, i) => {
    if (i === 0) return null;
    const prev = values[i - 1];
    if (v == null || prev == null || prev === 0 || prev < 0) return null;
    return ((v - prev) / prev) * 100;
  });
}

// 构造指标趋势图 option：柱状 = 指标值（左轴），折线 = 同比增速（右轴）。
// narrow 为窄屏布局：图例移底、字号 11px、grid 左右边距收紧、长指标名缩略。
// 增速全为 null（如仅 1 年数据）时不渲染折线系列与右 y 轴，避免图例与系列错配。
function buildIndicatorChartOption(it, years, values, growth, narrow) {
  const unit = it.unit || '';
  const hasGrowth = growth.some(v => v != null);
  const seriesName = legendLabel(it.name, narrow);
  const fontSize = narrow ? 11 : 12;
  const legendData = hasGrowth ? [seriesName, '同比增速'] : [seriesName];
  const legend = narrow
    ? { data: legendData, bottom: 0, left: 'center', itemGap: 12, textStyle: { color: '#5d5151', fontSize } }
    : { data: legendData, top: 0, textStyle: { color: '#5d5151', fontSize } };

  const yAxis = [{
    type: 'value',
    // 单位已由刻度标签承载，左轴不再设仅含单位符号的轴名（FR-1）
    axisLabel: { formatter: (v) => fmtAxisValue(v, unit), fontSize },
    splitLine: { lineStyle: { type: 'dashed' } },
  }];
  if (hasGrowth) {
    yAxis.push({
      type: 'value',
      name: '增速 %',
      nameGap: 8,
      nameTextStyle: { color: PALETTE.neutral, fontSize },
      axisLabel: { formatter: fmtAxisPct, fontSize },
      splitLine: { show: false },
    });
  }

  const series = [{
    name: seriesName,
    type: 'bar',
    data: values.map(v => v == null ? null : v),
    // FR-10：柱色按该指标信号取值（向好绿/走弱红/无信号中性灰），负值恒红
    itemStyle: { color: (p) => barColorOf(it.trend_signal, p.value), borderRadius: [3, 3, 0, 0] },
    barMaxWidth: 44,
  }];
  if (hasGrowth) {
    series.push({
      name: '同比增速',
      type: 'line',
      yAxisIndex: 1,
      data: growth,
      smooth: true,
      symbol: 'circle',
      symbolSize: 7,
      lineStyle: { width: 2, color: PALETTE.growth },
      itemStyle: { color: PALETTE.growth },
      connectNulls: false,
    });
  }

  const fmtVal = (v) => {
    if (v == null) return '—';
    if (unit === '元') return fmtYi(v);
    if (unit === '%') return v.toFixed(2) + '%';
    if (unit === '倍') return v.toFixed(2) + ' 倍';
    return v.toFixed(2);
  };

  return {
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'cross' },
      formatter: (params) => {
        const idx = params[0].dataIndex;
        let html = '<b>' + years[idx] + ' 年</b><br/>';
        html += it.name + '：' + fmtVal(values[idx]) + '<br/>';
        html += '同比增速：' + (growth[idx] == null ? '—' : growth[idx].toFixed(2) + '%');
        return html;
      },
    },
    legend,
    // containLabel: 轴刻度标签占位不计入边距，左右可压到 8/12px 而不会裁切「1.2亿」这类长刻度（FR-3）。
    grid: narrow
      ? { left: 8, right: 12, top: 28, bottom: 46, containLabel: true }
      : { left: 12, right: 16, top: 48, bottom: 24, containLabel: true },
    xAxis: {
      type: 'category',
      data: years.map(String),
      boundaryGap: true,
      axisLabel: { fontSize, hideOverlap: true },
    },
    yAxis,
    series,
  };
}

// AI 雷达图：容器可用宽度窄于此值时用移动端规格（收窄 radius、降字号）。
// 与指标趋势图的 CHART_NARROW_WIDTH 分开设常量：两个图表的尺寸特征不同（雷达图半径由宽度主导），
// 且雷达图容器在 ≤768px 视口下仍可能宽达 710px，按视口判断会把宽容器一并降规格。
const RADAR_NARROW_WIDTH = 420;
const RADAR_NAME_MAX = 4; // 轴名单行最多字数，超出按语义边界折两行（FR-10）
const RADAR_NAME_SUFFIXES = ['能力', '效率', '水平', '状况', '质量', '结构', '趋势'];

// 轴名折行：优先在语义后缀前断开（「现金获取能力」→「现金获取」+「能力」），
// 保证两行均 ≥2 字、不出现单字孤立行；无后缀可依时从中点折行（向左取整）。
// 规则只看字数与后缀词、不含任何维度名常量，维度名扩展时自适应（FR-11 异常）。
function radarAxisName(name) {
  const n = String(name == null ? '' : name).trim();
  if (n.length <= RADAR_NAME_MAX) return n;
  for (const suf of RADAR_NAME_SUFFIXES) {
    if (n.length > suf.length + 1 && n.endsWith(suf)) {
      const head = n.slice(0, n.length - suf.length);
      if (head.length >= 2) return head + '\n' + suf;
    }
  }
  const cut = Math.max(2, Math.floor(n.length / 2));
  return n.slice(0, cut) + '\n' + n.slice(cut);
}

// 构造五维雷达图 option。narrow 为移动端规格（radius 60%、轴名 12px）。
// 折行只放在 axisName.formatter：indicator[].name 保留原文，避免 \n 污染 tooltip 渲染。
function buildRadarOption(scores, narrow) {
  return {
    tooltip: {},
    radar: {
      indicator: scores.map(s => ({ name: s.dimension, max: 100 })),
      center: ['50%', '52%'],
      radius: narrow ? '60%' : '66%',
      splitArea: { areaStyle: { color: ['rgba(20,154,94,0.03)', 'rgba(20,154,94,0.06)'] } },
      axisName: {
        formatter: radarAxisName,
        color: '#606266',
        fontSize: narrow ? 12 : 13,
      },
    },
    series: [{
      type: 'radar',
      data: [{
        value: scores.map(s => s.score),
        name: '财务评分',
        symbolSize: 5,
        lineStyle: { color: PALETTE.good, width: 2 },
        itemStyle: { color: PALETTE.good },
        areaStyle: { color: 'rgba(20,154,94,0.18)' },
      }],
    }],
  };
}

const app = createApp({
  setup() {
    const code = ref('');
    const loading = ref(false);
    const error = ref('');
    const data = ref(null);

    // R2 三大报表状态
    const financials = ref(null);
    const activeTab = ref('income');
    const startYear = ref(null);
    const endYear = ref(null);
    const finLoading = ref(false);

    // R3 六维财务指标分析状态
    const analysis = ref(null);
    const analysisLoading = ref(false);
    const analysisError = ref('');
    const analysisActive = ref('investing');
    const analysisStartYear = ref(null);
    const analysisEndYear = ref(null);

    // 指标趋势图状态
    const chartVisible = ref(false);
    const chartIndicator = ref(null);
    const chartRef = ref(null);
    const chartNarrow = ref(false); // 当前图表是否处于窄屏布局（宽度 < 480px）
    let chartResizeTimer = null;
    const radarNarrow = ref(false); // 雷达图是否处于移动端规格（容器宽 < 420px）
    let radarResizeTimer = null;

    // R4 公司估值状态
    const valModels = [
      { key: 'zero', label: '零增长模型', desc: '自由现金流恒定，经营资产价值 = FCF ÷ 折现率' },
      { key: 'perpetual', label: '永续增长模型', desc: '自由现金流按固定增长率永续增长（戈登模型）' },
      { key: 'two_stage', label: '两阶段模型', desc: '先高增长若干年，之后进入永续增长' },
      { key: 'three_stage', label: '三阶段模型', desc: '高增长 → 过渡增长 → 永续增长三阶段' },
    ];
    const valModel = ref('perpetual');
    const valModelDesc = computed(() => {
      const m = valModels.find(x => x.key === valModel.value);
      return m ? m.desc : '';
    });
    const valDiscount = ref(8);   // 折现率 %
    const valG = ref(3);          // 永续增长率 %
    const valG1 = ref(10);        // 第一阶段增长率 %
    const valN1 = ref(5);         // 第一阶段年数
    const valG2 = ref(3);         // 第二阶段/稳定期增长率 %
    const valN2 = ref(5);         // 第二阶段年数
    const valG3 = ref(3);         // 第三阶段永续增长率 %
    const valFCFModes = [
      { key: 'latest', label: '最近一年' },
      { key: 'average', label: '近几年平均值' },
      { key: 'median', label: '近几年中位数' },
      { key: 'trim_mean', label: '去极值平均值' },
    ];
    const valFCFMode = ref('latest'); // 基期自由现金流选取方式
    const valFCFYears = ref(3);       // 选取年数（平均值/中位数）
    const valAdjustRD = ref(false);   // 是否调整研发费用
    const valuation = ref(null);
    const valLoading = ref(false);
    const valError = ref('');

    // R5 AI 智能分析状态（apikey 由服务端启动参数提供，前端无需输入）
    const aiLoading = ref(false);
    const aiError = ref('');
    const aiResult = ref(null);
    const aiRadarRef = ref(null);
    const aiReasoning = ref('low'); // 分析模式：low 简洁 / medium 标准 / high 详细
    const aiCache = new Map(); // AI 分析结果缓存（code|年份|模式 → 结果，同条件复用避免重复消耗 token）

    // 搜索历史（localStorage 持久化，最多保留 MAX_HISTORY 条）
    const MAX_HISTORY = 10;
    const HISTORY_KEY = 'stockSearchHistory';
    const history = ref([]);

    const currentYear = new Date().getFullYear();
    const yearOptions = computed(() => {
      const ys = [];
      for (let y = currentYear; y >= 1990; y--) ys.push(y);
      return ys;
    });

    // 解析响应 JSON；若返回的是 HTML（如网关/代理/反向代理的错误页）则抛出含片段的清晰错误，便于定位来源。
    async function readJSON(res) {
      const text = await res.text();
      const trimmed = text.trimStart();
      if (trimmed.startsWith('<')) {
        throw new Error('服务端返回了 HTML 页面而非 JSON（疑似网关/代理拦截），片段：' + trimmed.slice(0, 300));
      }
      try {
        return JSON.parse(text);
      } catch (e) {
        throw new Error('响应解析失败：' + e.message);
      }
    }

    async function fetchData() {
      loading.value = true;
      error.value = '';
      data.value = null;
      try {
        const res = await fetch(`/api/stock/${code.value.trim()}/indicators`);
        const json = await readJSON(res);
        if (json.code !== 0) {
          error.value = json.message || '查询失败';
          return;
        }
        data.value = json.data;
        addHistory(code.value.trim(), json.data.stock.name);
      } catch (e) {
        error.value = '请求失败：' + e.message;
      } finally {
        loading.value = false;
      }
    }

    async function fetchFinancials() {
      finLoading.value = true;
      try {
        const p = (startYear.value && endYear.value)
          ? `?startYear=${startYear.value}&endYear=${endYear.value}` : '';
        const res = await fetch(`/api/stock/${code.value.trim()}/financials${p}`);
        const json = await readJSON(res);
        if (json.code !== 0) return;
        financials.value = json.data;
        // 首次加载用返回范围回填年份选择
        if (!startYear.value && !endYear.value) {
          const ys = json.data.income.years || [];
          if (ys.length) {
            startYear.value = ys[0];
            endYear.value = ys[ys.length - 1];
          }
        }
      } catch (e) {
        // R2 单独失败不影响 R1，静默忽略
      } finally {
        finLoading.value = false;
      }
    }

    async function fetchAnalysis() {
      analysisLoading.value = true;
      analysisError.value = '';
      try {
        const p = (analysisStartYear.value && analysisEndYear.value)
          ? `?startYear=${analysisStartYear.value}&endYear=${analysisEndYear.value}` : '';
        const res = await fetch(`/api/stock/${code.value.trim()}/analysis${p}`);
        const json = await readJSON(res);
        if (json.code !== 0) {
          analysisError.value = json.message || '分析失败';
          return;
        }
        analysis.value = json.data;
        // 首次加载用返回范围回填年份选择
        if (!analysisStartYear.value && !analysisEndYear.value) {
          const ys = json.data.years || [];
          if (ys.length) {
            analysisStartYear.value = ys[0];
            analysisEndYear.value = ys[ys.length - 1];
          }
        }
        // 默认展开第一个已实现的维度，而非硬编码 'investing'
        const firstDone = (json.data.dimensions || []).find(d => d.status === 'done');
        analysisActive.value = firstDone ? firstDone.key : 'investing';
      } catch (e) {
        analysisError.value = '请求失败：' + e.message;
      } finally {
        analysisLoading.value = false;
      }
    }

    function changeAnalysisRange() {
      if (analysisStartYear.value && analysisEndYear.value && analysisStartYear.value > analysisEndYear.value) {
        [analysisStartYear.value, analysisEndYear.value] = [analysisEndYear.value, analysisStartYear.value];
      }
      fetchAnalysis();
    }

    // 点击指标行上的趋势图图标，弹出该指标的折线图。
    function showIndicatorChart(it) {
      const hasData = (it.values || []).some(v => v != null);
      if (!hasData) {
        ElementPlus.ElMessage.info('该指标暂无数据');
        return;
      }
      chartIndicator.value = it;
      chartVisible.value = true;
    }

    // 在弹窗打开后渲染指标图：柱状=指标值，折线=同比增速（双轴）。
    // 是否窄屏由图表容器实测宽度决定（弹窗宽度非固定），据此选图例位置、字号与边距。
    function renderIndicatorChart() {
      if (!chartRef.value || !chartIndicator.value || !analysis.value) return;
      const el = chartRef.value;
      const values = chartIndicator.value.values || [];
      const growth = growthSeries(values);

      let chart = echarts.getInstanceByDom(el);
      if (chart) chart.dispose();
      chart = echarts.init(el);
      chartNarrow.value = el.clientWidth < CHART_NARROW_WIDTH;
      chart.setOption(buildIndicatorChartOption(
        chartIndicator.value, analysis.value.years || [], values, growth, chartNarrow.value));
    }

    // 窗口 resize / 手机横竖屏切换：防抖后重算布局；跨 480px 阈值时整体重设 option（图例位置/grid/字号需重算）。
    function handleChartResize() {
      if (!chartVisible.value || !chartRef.value) return;
      clearTimeout(chartResizeTimer);
      chartResizeTimer = setTimeout(() => {
        if (!chartVisible.value || !chartRef.value) return;
        const el = chartRef.value;
        const chart = echarts.getInstanceByDom(el);
        if (!chart) return;
        const next = el.clientWidth < CHART_NARROW_WIDTH;
        if (next !== chartNarrow.value) {
          chartNarrow.value = next;
          const values = (chartIndicator.value && chartIndicator.value.values) || [];
          chart.setOption(buildIndicatorChartOption(
            chartIndicator.value, (analysis.value && analysis.value.years) || [],
            values, growthSeries(values), next));
        }
        chart.resize();
      }, 120);
    }

    // 弹窗关闭时销毁图表实例，避免实例挂在已移除的 DOM 上。
    function disposeIndicatorChart() {
      if (!chartRef.value) return;
      const chart = echarts.getInstanceByDom(chartRef.value);
      if (chart) chart.dispose();
    }

    async function fetchValuation() {
      if (!code.value.trim()) return;
      valLoading.value = true;
      valError.value = '';
      try {
        const q = new URLSearchParams({ model: valModel.value, r: valDiscount.value });
        if (valModel.value === 'perpetual') q.set('g', valG.value);
        if (valModel.value === 'two_stage') {
          q.set('g1', valG1.value); q.set('n1', valN1.value); q.set('g2', valG2.value);
        }
        if (valModel.value === 'three_stage') {
          q.set('g1', valG1.value); q.set('n1', valN1.value);
          q.set('g2', valG2.value); q.set('n2', valN2.value); q.set('g3', valG3.value);
        }
        q.set('fcf_mode', valFCFMode.value);
        if (valFCFMode.value !== 'latest') q.set('fcf_years', valFCFYears.value);
        q.set('adjust_rd', valAdjustRD.value ? 'true' : 'false');
        const res = await fetch(`/api/stock/${code.value.trim()}/valuation?${q.toString()}`);
        const json = await readJSON(res);
        if (json.code !== 0) {
          valError.value = json.message || '估值失败';
          valuation.value = null;
          return;
        }
        valuation.value = json.data;
      } catch (e) {
        valError.value = '请求失败：' + e.message;
        valuation.value = null;
      } finally {
        valLoading.value = false;
      }
    }

    function doSearch() {
      // 切换股票时清空上一支股票的 AI 分析结果，避免残留
      aiResult.value = null;
      aiError.value = '';
      aiLoading.value = false;
      fetchData(); fetchFinancials(); fetchAnalysis(); fetchValuation();
    }

    // 空态示例代码点击：填入代码并直接查询。
    function pickCode(c) {
      code.value = c;
      doSearch();
    }

    // AI 一键分析：把财务指标发给大模型，返回评分/结论/行业/估值推荐。
    async function fetchAIAnalysis() {
      if (!code.value.trim()) return;
      const reqCode = code.value.trim();
      const cacheKey = `${reqCode}|${analysisStartYear.value || ''}|${analysisEndYear.value || ''}|${aiReasoning.value || 'low'}`;
      const cached = aiCache.get(cacheKey);
      if (cached) {
        // 同条件已分析过，直接复用结果，不重复调用大模型
        aiResult.value = cached;
        await nextTick();
        renderRadar();
        return;
      }
      aiLoading.value = true;
      aiError.value = '';
      aiResult.value = null;
      try {
        const params = [];
        if (analysisStartYear.value && analysisEndYear.value) {
          params.push(`startYear=${analysisStartYear.value}`, `endYear=${analysisEndYear.value}`);
        }
        params.push(`reasoning=${aiReasoning.value || 'low'}`);
        const res = await fetch(`/api/stock/${reqCode}/ai-analysis?${params.join('&')}`);
        const json = await readJSON(res);
        if (code.value.trim() !== reqCode) return; // 期间切换了股票，丢弃旧结果
        if (json.code !== 0) {
          aiError.value = json.message || 'AI 分析失败';
          return;
        }
        aiCache.set(cacheKey, json.data);
        aiResult.value = json.data;
        await nextTick();
        renderRadar();
      } catch (e) {
        if (code.value.trim() !== reqCode) return;
        aiError.value = '请求失败：' + e.message;
      } finally {
        if (code.value.trim() === reqCode) aiLoading.value = false;
      }
    }

    // 绘制财务指标五维雷达图（维度 × 百分制评分）。
    // 窄屏判据取容器实测宽度（卡片内边距决定，非视口推断），跨阈值时整体重设 option。
    function renderRadar() {
      if (!aiRadarRef.value || !aiResult.value) return;
      const el = aiRadarRef.value;
      radarNarrow.value = el.clientWidth < RADAR_NARROW_WIDTH;
      let chart = echarts.getInstanceByDom(el);
      if (chart) chart.dispose();
      chart = echarts.init(el);
      chart.setOption(buildRadarOption(aiResult.value.scores || [], radarNarrow.value));
    }

    // 窗口 resize / 手机横竖屏切换：防抖后跨阈值重设 option，并 resize() 重新测量容器高度。
    function handleRadarResize() {
      if (!aiResult.value || !aiRadarRef.value) return;
      clearTimeout(radarResizeTimer);
      radarResizeTimer = setTimeout(() => {
        if (!aiResult.value || !aiRadarRef.value) return;
        const el = aiRadarRef.value;
        const chart = echarts.getInstanceByDom(el);
        if (!chart) return;
        const next = el.clientWidth < RADAR_NARROW_WIDTH;
        if (next !== radarNarrow.value) {
          radarNarrow.value = next;
          chart.setOption(buildRadarOption(aiResult.value.scores || [], next));
        }
        chart.resize(); // 容器高度随 CSS 断点变化后重新测量
      }, 120);
    }

    // 按评分档位取进度条颜色（FR-9：绿=优秀 → 红=较弱，色相带连续）。
    function scoreColor(score) {
      if (score >= 90) return PALETTE.good;    // 90–100 优秀：绿
      if (score >= 75) return PALETTE.goodMid; // 75–89  良好：黄绿
      if (score >= 60) return PALETTE.warn;    // 60–74  一般：琥珀（不变）
      return PALETTE.bad;                      // <60    较弱：红
    }

    // 数值显示：整数不带小数，其余保留两位。
    function fmtNum(v) {
      if (v == null || isNaN(v)) return '';
      return Number.isInteger(v) ? String(v) : v.toFixed(2);
    }

    // FR-7/FR-8：研发调整开关单向联动 —— 推荐为 true 时打开；
    // false / 字段缺失时保持用户当前选择（不主动关闭）。
    // 用 === true 严格判断：旧响应 / 缓存无该字段时为 undefined → 保持现状；字符串脏数据也不会误开。
    function resolveAdjustRD(current, recommended) {
      return recommended === true ? true : current;
    }

    // 把 AI 推荐的估值模型与参数套用到「公司估值」表单并计算。
    function applyAIValuation(v) {
      if (!v || !v.model) return;
      valModel.value = v.model;
      if (v.discount_rate) valDiscount.value = v.discount_rate;
      const params = v.params || [];
      const get = (key) => {
        const p = params.find(x => x.key === key);
        return p ? p.value : null;
      };
      if (get('g') != null) valG.value = get('g');
      if (get('g1') != null) valG1.value = get('g1');
      if (get('n1') != null) valN1.value = Math.round(get('n1'));
      if (get('g2') != null) valG2.value = get('g2');
      if (get('n2') != null) valN2.value = Math.round(get('n2'));
      if (get('g3') != null) valG3.value = get('g3');
      // ref 写入同步生效，紧随其后的 fetchValuation() 读到的已是新值，故开关与请求参数一致。
      valAdjustRD.value = resolveAdjustRD(valAdjustRD.value, v.adjust_rd);
      fetchValuation();
      ElementPlus.ElMessage.success('已套用 AI 推荐的估值参数并计算');
    }

    function changeRange() {
      if (startYear.value && endYear.value && startYear.value > endYear.value) {
        [startYear.value, endYear.value] = [endYear.value, startYear.value];
      }
      fetchFinancials();
    }

    function fmt(v, unit) {
      if (v == null || isNaN(v)) return '-';
      if (unit === '%') return v.toFixed(2) + '%';
      if (unit === '元') return v.toFixed(2) + ' 元';
      if (unit === '倍') return v.toFixed(2) + ' 倍';
      if (unit === '亿元') return v.toFixed(2) + ' 亿元';
      return v.toFixed(2) + (unit ? ' ' + unit : '');
    }

    // R3 分析指标数值格式化：按单位区分（元→亿/万、%→百分比）
    function fmtAnalysisVal(v, unit) {
      if (v == null || isNaN(v)) return '—';
      if (unit === '%') return v.toFixed(2) + '%';
      if (unit === '元') return fmtYi(v);
      return v.toFixed(2) + (unit ? ' ' + unit : '');
    }

    // FR-5 / FR-16：信号说明文案。phrase = 该指标所属小节标题（方向解读短语，可为空）；
    // dimStatus = 所属维度状态（决策更新 2 新增，非 'done' 时给维度级灰点文案）。
    // **三态都必须返回非空文案**（FR-5 / AC-10）：绿/红 → 幅度分级措辞；灰 → 四类温和文案。
    // 措辞分级只读后端 trend_rate（单一真值，不在前端重算 R）：|R| < MILD_CHANGE_THRESHOLD 用温和词。
    function trendTip(it, years, phrase, dimStatus) {
      const name = it.name || '该指标';
      // ① 维度非 'done'（FR-12①）：整维度不参与趋势判定。当前模板不渲染非 done 维度的指标行，属防御分支。
      if (dimStatus && dimStatus !== 'done') return `${name}：该维度暂无数据，暂无法判断趋势`;
      const vs = it.values || [];
      let i1 = -1, im = -1;
      for (let i = 0; i < vs.length; i++) {
        if (vs[i] != null) { if (i1 < 0) i1 = i; im = i; }
      }
      // ② 数据不足（FR-12②③⑥⑦）：R 不可计算（缺字段/非数字）或有效点 < 2。
      //    注意用 typeof 判缺失而非真值判断——trend_rate === 0（首末持平）是合法值。
      const rate = it.trend_rate;
      if (typeof rate !== 'number' || !isFinite(rate) || i1 < 0 || i1 === im) {
        return `${name}在所选范围内数据不足，暂无法判断趋势`;
      }
      const s1 = vs[i1], sm = vs[im];
      const ys = years || [];
      const span = (ys[im] != null && ys[i1] != null) ? (ys[im] - ys[i1] + 1) : (im - i1 + 1);
      const pct = `${rate >= 0 ? '+' : ''}${rate.toFixed(1)}%`;
      const vals = (sm === s1)
        ? `保持在 ${fmtAnalysisVal(s1, it.unit)}`                                          // R=0：避免「由 X 降至 X」
        : `由 ${fmtAnalysisVal(s1, it.unit)} ${sm > s1 ? '升至' : '降至'} ${fmtAnalysisVal(sm, it.unit)}`;
      const s = it.trend_signal;
      // ③ 灰点（无信号）：温和陈述，禁用「向好/走弱/改善」评价词（FR-5）。
      if (s !== 'improving' && s !== 'worsening') {
        // 方向 neutral（可算出 R）→ 用「无明确好坏方向」；方向有向且无信号 → 必为趋势平稳（|R| < 5%）。
        if (it.direction === 'neutral') return `近 ${span} 年${name}${vals}（${pct}），该指标无明确好坏方向`;
        return `近 ${span} 年${name}变化不大（${pct}），趋势平稳`;
      }
      // ④ 绿点/红点：末词按**向好/走弱极性**取（FR-16 Q10.1），数值动向已由「升至/降至」表达。
      const lead = (phrase && phrase !== it.name) ? phrase : ''; // 与指标名重复时省略短语
      const mild = Math.abs(rate) < MILD_CHANGE_THRESHOLD;
      const concl = (s === 'improving') ? (mild ? '略有改善' : '向好') : (mild ? '略有走弱' : '走弱');
      return `近 ${span} 年${name}${vals}（${pct}），${lead}${concl}`;
    }

    // ★决策更新 2（FR-4）：信号灯圆点类名——向好 improving / 走弱 worsening / 其余一律 neutral（灰）。
    // 三态全覆盖：任何指标都必须落到这三个类之一，故**不存在空类**（AC-24 每行恰一个圆点）。
    // trend_rate 守卫：后端不变式为「有信号 ⟺ trend_rate 非 nil」，故该守卫只对**旧响应/脏数据**生效，
    // 此时保守降级为灰，保证「圆点颜色」与「tooltip 文案（数据不足）」不会自相矛盾。
    function trendDotClass(it) {
      if (typeof it.trend_rate !== 'number') return 'neutral';
      return (it.trend_signal === 'improving' || it.trend_signal === 'worsening') ? it.trend_signal : 'neutral';
    }

    // 同比文本：范围内最早年/上期无数据/上期为0 →「—」；上期为负 → 不显示百分比
    function yoyText(values, i) {
      if (i === 0) return '—';
      const prev = values[i - 1], cur = values[i];
      if (cur == null || prev == null) return '—';
      if (prev === 0) return '—';
      if (prev < 0) return '';
      return (((cur - prev) / prev) * 100).toFixed(2) + '%';
    }

    // 同比涨跌色：A股红涨绿跌（三大报表专用，FR-8 保持现状）
    function yoyClass(values, i) {
      if (i === 0) return '';
      const prev = values[i - 1], cur = values[i];
      if (cur == null || prev == null || prev === 0 || prev < 0) return '';
      return (cur - prev) / prev >= 0 ? 'up' : 'down';
    }

    // FR-7：方向感知同比色（六维分析表专用）。返回 'good' / 'bad' / ''（无色）。
    // 与 yoyText 的显示规则一致：首年、任一年缺失、上年为 0 或负 → 无色；
    // 与信号灯不同，此处比较相邻两年（逐年信息），且仅看符号（持平即无色），不套用 5% 阈值。
    function yoyClassDir(values, i, direction) {
      if (i === 0) return '';
      const prev = values[i - 1], cur = values[i];
      if (cur == null || prev == null || prev === 0 || prev < 0) return '';
      if (direction !== 'higher_better' && direction !== 'lower_better') return ''; // 中性方向一律不着色
      if (cur === prev) return '';                                                  // 持平 → 无色
      const up = cur > prev;
      return (direction === 'higher_better') === up ? 'good' : 'bad';
    }

    // 指标层级行样式：sub=子项（缩进浅色）；net/subtotal=合计/净额（加粗+上分隔线）
    function indRowClass(kind) {
      if (kind === 'sub') return 'sub-row';
      if (kind === 'net' || kind === 'subtotal') return 'agg-row';
      return '';
    }

    function stmtYears() {
      if (!financials.value) return [];
      const s = financials.value[activeTab.value];
      return s ? s.years : [];
    }
    function stmtGroups() {
      if (!financials.value) return [];
      const s = financials.value[activeTab.value];
      if (!s) return [];
      if (s.groups && s.groups.length) return s.groups;
      return [{ title: '', items: s.items || [] }];
    }
    function hasStmtData() {
      return stmtGroups().some(g => g.items && g.items.length > 0);
    }

    function loadHistory() {
      try {
        const raw = localStorage.getItem(HISTORY_KEY);
        history.value = raw ? JSON.parse(raw) : [];
      } catch (e) {
        history.value = [];
      }
    }
    function saveHistory() {
      try { localStorage.setItem(HISTORY_KEY, JSON.stringify(history.value)); } catch (e) {}
    }
    function addHistory(cd, name) {
      if (!cd) return;
      const list = history.value.filter(h => h.code !== cd);
      list.unshift({ code: cd, name: name || '' });
      if (list.length > MAX_HISTORY) list.length = MAX_HISTORY; // 超限丢弃最早
      history.value = list;
      saveHistory();
    }
    function clearHistory() {
      history.value = [];
      saveHistory();
    }
    function querySearch(query, cb) {
      const q = (query || '').trim();
      cb(history.value
        .filter(h => !q || h.code.includes(q) || (h.name && h.name.includes(q)))
        .map(h => ({ value: h.code, name: h.name })));
    }
    function handleSelect(item) {
      code.value = item.value;
      doSearch();
    }

    // 移动端手势：在财务指标分析区域左右滑动切换维度标签
    const tabSwipe = { on: false, x: 0, y: 0 };
    function tabTouchStart(e) {
      const t = e.changedTouches[0];
      // 横向滚动的表格区域不触发切标签，保留表格自身的左右滑动
      tabSwipe.on = !e.target.closest('.table-wrap');
      tabSwipe.x = t.clientX;
      tabSwipe.y = t.clientY;
    }
    function tabTouchEnd(e) {
      if (!tabSwipe.on) return;
      const t = e.changedTouches[0];
      const dx = t.clientX - tabSwipe.x;
      const dy = t.clientY - tabSwipe.y;
      if (Math.abs(dx) < 40 || Math.abs(dx) < Math.abs(dy)) return;
      const dims = (analysis.value && analysis.value.dimensions) || [];
      const idx = dims.findIndex(d => d.key === analysisActive.value);
      if (idx < 0) return;
      if (dx < 0 && idx < dims.length - 1) analysisActive.value = dims[idx + 1].key;
      else if (dx > 0 && idx > 0) analysisActive.value = dims[idx - 1].key;
    }

    loadHistory();

    // 单页应用，监听器与页面同生命周期，无需解绑（FR-5：窗口 resize / 横竖屏切换重排）。
    window.addEventListener('resize', handleChartResize);
    window.addEventListener('resize', handleRadarResize);

    return {
      code, loading, error, data, fetchData,
      financials, activeTab, startYear, endYear, yearOptions, finLoading,
      doSearch, pickCode, changeRange, fmtYi, yoyText, yoyClass, stmtYears, stmtGroups, hasStmtData,
      fmt,
      analysis, analysisLoading, analysisError, analysisActive, fmtAnalysisVal, indRowClass,
      yoyClassDir, trendTip, trendDotClass, barColorOf, PALETTE, MILD_CHANGE_THRESHOLD,
      tabTouchStart, tabTouchEnd,
      analysisStartYear, analysisEndYear, changeAnalysisRange,
      chartVisible, chartIndicator, chartRef, chartNarrow, showIndicatorChart, renderIndicatorChart, disposeIndicatorChart,
      valModels, valModel, valModelDesc, valDiscount, valG, valG1, valN1, valG2, valN2, valG3,
      valFCFModes, valFCFMode, valFCFYears, valAdjustRD,
      valuation, valLoading, valError, fetchValuation,
      aiLoading, aiError, aiResult, aiRadarRef, aiReasoning, fetchAIAnalysis, renderRadar,
      radarAxisName, buildRadarOption, resolveAdjustRD,
      scoreColor, fmtNum, applyAIValuation,
      history, querySearch, handleSelect, clearHistory,
    };
  },
  template: `
<div class="page">
      <header class="topbar">
        <div class="topbar-inner">
          <div class="brand">
            <span class="brand-mark">财</span>
            <span class="brand-name">A股财报分析</span>
          </div>
          <div class="searchbar">
            <el-autocomplete
              v-model="code"
              :fetch-suggestions="querySearch"
              placeholder="输入A股代码，如 600519"
              clearable
              :trigger-on-focus="true"
              @select="handleSelect"
              @keyup.enter="doSearch"
            >
              <template #default="{ item }">
                <div class="hist-item">
                  <span class="hist-code">{{ item.value }}</span>
                  <span class="hist-name">{{ item.name }}</span>
                </div>
              </template>
            </el-autocomplete>
            <el-button type="primary" :loading="loading" @click="doSearch">查询</el-button>
            <button v-if="history.length" class="clear-hist-btn" title="清空搜索历史" @click="clearHistory">
              <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <polyline points="3 6 5 6 21 6"></polyline>
                <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"></path>
                <path d="M10 11v6M14 11v6"></path>
                <path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"></path>
              </svg>
            </button>
          </div>
        </div>
        <div v-if="error" class="topbar-alert">
          <el-alert :title="error" type="error" :closable="false" />
        </div>
      </header>

      <main class="main">
        <section v-if="!data" class="welcome">
          <div class="welcome-title">读懂一家公司，从财报开始</div>
          <div class="welcome-desc">输入 A 股代码，查看核心指标、三大报表、六维财务分析、估值与 AI 智能诊断。</div>
          <div class="welcome-hint">
            <span>试试：</span>
            <button class="code-chip" @click="pickCode('600519')">600519 贵州茅台</button>
            <button class="code-chip" @click="pickCode('300750')">300750 宁德时代</button>
            <button class="code-chip" @click="pickCode('688012')">688012 中微公司</button>
          </div>
        </section>

        <template v-if="data">
          <section class="card stock-card">
            <div class="stock-main">
              <div class="stock-id">
                <div class="stock-name">{{ data.stock.name }}</div>
                <div class="stock-code">{{ data.stock.code }}</div>
              </div>
              <div class="stock-quote">
                <div :class="['stock-price', data.quote.change_pct >= 0 ? 'up' : 'down']">{{ data.quote.price.toFixed(2) }}</div>
                <div :class="['stock-chg', data.quote.change_pct >= 0 ? 'up' : 'down']">{{ (data.quote.change_pct >= 0 ? '+' : '') + data.quote.change_pct.toFixed(2) }}%</div>
                <div class="stock-cap">总市值 {{ fmtYi(data.quote.market_cap) }}</div>
              </div>
            </div>
          </section>

          <section class="card">
            <div class="card-head"><h2 class="card-title">核心指标<span class="sub">最新年报</span></h2></div>
            <div class="card-body">
              <div class="stat-grid">
                <div v-for="it in data.indicators" :key="it.name" class="stat">
                  <div class="stat-name">{{ it.name }}</div>
                  <div class="stat-value">{{ fmt(it.value, it.unit) }}</div>
                </div>
              </div>
            </div>
          </section>

          <section class="card" v-loading="finLoading">
            <div class="card-head">
              <h2 class="card-title">三大报表<span class="sub">年报</span></h2>
              <div class="year-filter">
                <el-select v-model="startYear" placeholder="起始年" @change="changeRange">
                  <el-option v-for="y in yearOptions" :key="y" :label="y" :value="y" />
                </el-select>
                <span class="sep">—</span>
                <el-select v-model="endYear" placeholder="结束年" @change="changeRange">
                  <el-option v-for="y in yearOptions" :key="y" :label="y" :value="y" />
                </el-select>
              </div>
            </div>
            <div class="card-body">
              <el-tabs v-model="activeTab">
                <el-tab-pane label="利润表" name="income" />
                <el-tab-pane label="资产负债表" name="balance" />
                <el-tab-pane label="现金流量表" name="cashflow" />
              </el-tabs>
              <el-empty v-if="!hasStmtData()" description="所选年份范围无数据" />
              <div v-else class="table-wrap">
                <div v-for="g in stmtGroups()" :key="g.title || 'main'" class="stmt-group">
                  <div v-if="g.title" class="group-title">{{ g.title }}</div>
                  <table class="stmt-table">
                    <thead>
                      <tr>
                        <th class="item-col">科目</th>
                        <th v-for="y in stmtYears()" :key="y">{{ y }}</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="it in g.items" :key="it.field">
                        <td class="item-name"><el-tooltip :content="it.name" placement="top" :trigger="['hover','focus']"><span class="stmt-text" tabindex="0">{{ it.name }}</span></el-tooltip></td>
                        <td v-for="(v, i) in it.values" :key="i" class="num">
                          <div class="val">{{ fmtYi(v) }}</div>
                          <div class="yoy" :class="yoyClass(it.values, i)">{{ yoyText(it.values, i) }}</div>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </div>
            </div>
          </section>

          <section class="card" v-loading="analysisLoading">
            <div class="card-head">
              <h2 class="card-title">财务指标分析<span class="sub">六维</span></h2>
              <div class="year-filter">
                <el-select v-model="analysisStartYear" placeholder="起始年" @change="changeAnalysisRange">
                  <el-option v-for="y in yearOptions" :key="y" :label="y" :value="y" />
                </el-select>
                <span class="sep">—</span>
                <el-select v-model="analysisEndYear" placeholder="结束年" @change="changeAnalysisRange">
                  <el-option v-for="y in yearOptions" :key="y" :label="y" :value="y" />
                </el-select>
              </div>
            </div>
            <div class="card-body">
              <el-alert v-if="analysisError" :title="analysisError" type="warning" style="margin-bottom:12px" />
              <div @touchstart="tabTouchStart" @touchend="tabTouchEnd">
                <el-tabs v-if="analysis" v-model="analysisActive">
                  <el-tab-pane v-for="dim in analysis.dimensions" :key="dim.key" :name="dim.key">
                    <template #label>
                      <span>{{ dim.name }}</span>
                      <el-tag v-if="dim.status === 'pending'" size="small" type="info" style="margin-left:6px">待补充</el-tag>
                      <el-tag v-else-if="dim.status === 'no_data'" size="small" type="warning" style="margin-left:6px">无年报数据</el-tag>
                    </template>
                    <template v-if="dim.status === 'done'">
                      <el-alert v-if="dim.notes && dim.notes.length" type="warning" title="重分类口径提示" :closable="false" style="margin-bottom:12px">
                        <div v-for="(n, i) in dim.notes" :key="i" style="font-size:12px;line-height:1.6">{{ n }}</div>
                      </el-alert>
                      <div v-for="s in dim.sections" :key="s.title" class="stmt-group">
                        <div class="group-title">{{ s.title }}</div>
                        <div class="table-wrap">
                          <table class="stmt-table analysis-table">
                            <thead>
                              <tr>
                                <th class="item-col">指标</th>
                                <th v-for="y in analysis.years" :key="y">{{ y }}</th>
                              </tr>
                            </thead>
                            <tbody>
                              <tr v-for="it in s.indicators" :key="it.key" :class="indRowClass(it.kind)">
                                <td class="item-name">
                                  <div class="ind-name">
                                    <button class="chart-btn" title="查看趋势" @click="showIndicatorChart(it)">
                                      <svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="2,12 6,7 9,9 14,3"></polyline></svg>
                                    </button>
                                    <!-- ★R9 决策更新 2：趋势信号灯**三态全渲染**（向好绿 / 走弱红 / 其余灰），无 v-if（AC-24） -->
                                    <el-tooltip :content="trendTip(it, analysis.years, s.title, dim.status)" placement="top" :trigger="['hover','focus']" popper-class="ind-tip">
                                      <span class="trend-dot" :class="trendDotClass(it)" tabindex="0" role="img" :aria-label="trendTip(it, analysis.years, s.title, dim.status)"></span>
                                    </el-tooltip>
                                    <el-tooltip :content="it.interpretation ? (it.name + '：' + it.interpretation) : it.name" placement="top" :trigger="['hover','focus']" popper-class="ind-tip"><span class="ind-text" tabindex="0">{{ it.name }}</span></el-tooltip>
                                    <el-tooltip v-if="it.interpretation" :content="it.interpretation" placement="top" :show-after="200" popper-class="ind-tip">
                                      <span class="help-icon">?</span>
                                    </el-tooltip>
                                    <el-tooltip v-if="it.note" :content="it.note" placement="top" :trigger="['hover','focus']" popper-class="ind-tip">
                                      <span class="warn-icon" tabindex="0">!</span>
                                    </el-tooltip>
                                  </div>
                                </td>
                                <td v-for="(v, i) in it.values" :key="i" class="num">
                                  <div class="val">{{ fmtAnalysisVal(v, it.unit) }}</div>
                                  <!-- 六维分析表：方向感知（绿=向好、红=走弱、中性/上年≤0 不着色） -->
                                  <div class="yoy" :class="yoyClassDir(it.values, i, it.direction)">{{ yoyText(it.values, i) }}</div>
                                </td>
                              </tr>
                            </tbody>
                          </table>
                        </div>
                      </div>
                    </template>
                    <div v-else-if="dim.status === 'pending'" class="pending-hint">该维度分析指标待补充（见 docs/公司财务指标分析.md）</div>
                    <div v-else class="pending-hint">该维度暂无年报数据</div>
                  </el-tab-pane>
                </el-tabs>
              </div>
              <el-dialog v-model="chartVisible" :title="chartIndicator ? chartIndicator.name : ''" width="min(680px, 92vw)" destroy-on-close @opened="renderIndicatorChart" @close="disposeIndicatorChart">
                <div ref="chartRef" class="chart-box"></div>
              </el-dialog>
            </div>
          </section>

          <section class="card" v-loading="valLoading">
            <div class="card-head"><h2 class="card-title">公司估值<span class="sub">现金流贴现</span></h2></div>
            <div class="card-body">
              <div class="val-form">
                <div class="val-block">
                  <div class="val-block-title">模型与折现率</div>
                  <div class="val-fields">
                    <div class="val-field">
                      <span class="val-label">估值模型</span>
                      <el-select v-model="valModel" style="width:160px">
                        <el-option v-for="m in valModels" :key="m.key" :label="m.label" :value="m.key" />
                      </el-select>
                    </div>
                    <div class="val-field">
                      <span class="val-label">折现率</span>
                      <el-input-number v-model="valDiscount" :min="0.1" :max="100" :step="0.5" :precision="1" />
                      <span class="val-unit">%</span>
                    </div>

                    <div class="val-field" v-if="valModel === 'perpetual'">
                      <span class="val-label">永续增长率</span>
                      <el-input-number v-model="valG" :min="-50" :max="50" :step="0.5" :precision="1" />
                      <span class="val-unit">%</span>
                    </div>

                    <template v-if="valModel === 'two_stage'">
                      <div class="val-field">
                        <span class="val-label">高增长期增长率</span>
                        <el-input-number v-model="valG1" :min="-50" :max="100" :step="0.5" :precision="1" />
                        <span class="val-unit">%</span>
                      </div>
                      <div class="val-field">
                        <span class="val-label">高增长期年数</span>
                        <el-input-number v-model="valN1" :min="1" :max="50" :step="1" />
                        <span class="val-unit">年</span>
                      </div>
                      <div class="val-field">
                        <span class="val-label">永续增长率</span>
                        <el-input-number v-model="valG2" :min="-50" :max="50" :step="0.5" :precision="1" />
                        <span class="val-unit">%</span>
                      </div>
                    </template>

                    <template v-if="valModel === 'three_stage'">
                      <div class="val-field">
                        <span class="val-label">第一阶段增长率</span>
                        <el-input-number v-model="valG1" :min="-50" :max="100" :step="0.5" :precision="1" />
                        <span class="val-unit">%</span>
                      </div>
                      <div class="val-field">
                        <span class="val-label">第一阶段年数</span>
                        <el-input-number v-model="valN1" :min="1" :max="50" :step="1" />
                        <span class="val-unit">年</span>
                      </div>
                      <div class="val-field">
                        <span class="val-label">第二阶段增长率</span>
                        <el-input-number v-model="valG2" :min="-50" :max="100" :step="0.5" :precision="1" />
                        <span class="val-unit">%</span>
                      </div>
                      <div class="val-field">
                        <span class="val-label">第二阶段年数</span>
                        <el-input-number v-model="valN2" :min="1" :max="50" :step="1" />
                        <span class="val-unit">年</span>
                      </div>
                      <div class="val-field">
                        <span class="val-label">永续增长率</span>
                        <el-input-number v-model="valG3" :min="-50" :max="50" :step="0.5" :precision="1" />
                        <span class="val-unit">%</span>
                      </div>
                    </template>
                  </div>
                  <div class="val-desc">{{ valModelDesc }}</div>
                </div>

                <div class="val-block">
                  <div class="val-block-title">自由现金流假设</div>
                  <div class="val-fields">
                    <div class="val-field">
                      <span class="val-label">基期现金流</span>
                      <el-select v-model="valFCFMode" style="width:150px">
                        <el-option v-for="m in valFCFModes" :key="m.key" :label="m.label" :value="m.key" />
                      </el-select>
                    </div>
                    <div class="val-field" v-if="valFCFMode !== 'latest'">
                      <span class="val-label">选取年数</span>
                      <el-input-number v-model="valFCFYears" :min="1" :max="10" :step="1" />
                      <span class="val-unit">年</span>
                    </div>
                    <div class="val-field">
                      <span class="val-label">研发调整</span>
                      <el-switch v-model="valAdjustRD" />
                    </div>
                  </div>
                  <div class="val-desc">研发调整：适用成长科技股，把研发投入扩张部分加回自由现金流。</div>
                </div>

                <div class="val-actions">
                  <el-button type="primary" :loading="valLoading" @click="fetchValuation">计算估值</el-button>
                </div>
              </div>
              <el-alert v-if="valError" :title="valError" type="warning" :closable="false" style="margin-top:12px" />
              <template v-if="valuation">
                <div class="val-meta">{{ valuation.year }} 年报 · {{ valuation.model_name }} · 折现率 {{ valuation.discount_rate }}% · 基期FCF取{{ valuation.fcf_mode_name }}<template v-if="valuation.fcf_years">（{{ valuation.fcf_years }}年）</template></div>
                <table class="stmt-table val-table">
                  <tbody>
                    <tr>
                      <td class="item-name">经营资产自由现金流（基期）</td>
                      <td class="num">
                        {{ fmtYi(valuation.base_fcf) }}
                        <span class="val-hint" v-if="valuation.adjust_rd && valuation.rd_adjustment">（含研发扩张加回 {{ fmtYi(valuation.rd_adjustment) }}）</span>
                      </td>
                    </tr>
                    <tr>
                      <td class="item-name">金融资产价值（账面）</td>
                      <td class="num">{{ fmtYi(valuation.financial_asset_value) }}</td>
                    </tr>
                    <tr>
                      <td class="item-name">长期股权投资价值</td>
                      <td class="num">
                        {{ fmtYi(valuation.long_equity_value) }}
                        <span class="val-hint" v-if="valuation.long_equity_return != null">（账面 {{ fmtYi(valuation.long_equity_book) }}，收益率 {{ valuation.long_equity_return.toFixed(2) }}%）</span>
                      </td>
                    </tr>
                    <tr>
                      <td class="item-name">经营资产价值（DCF）</td>
                      <td class="num">{{ fmtYi(valuation.operating_asset_value) }}</td>
                    </tr>
                    <tr class="agg-row">
                      <td class="item-name">公司价值</td>
                      <td class="num">{{ fmtYi(valuation.company_value) }}</td>
                    </tr>
                    <tr>
                      <td class="item-name">减：债务价值（有息债务）</td>
                      <td class="num">{{ fmtYi(valuation.debt_value) }}</td>
                    </tr>
                    <tr class="agg-row">
                      <td class="item-name">股权价值</td>
                      <td class="num">{{ fmtYi(valuation.equity_value) }}</td>
                    </tr>
                    <tr v-if="valuation.equity_value_per_share">
                      <td class="item-name">每股股权价值</td>
                      <td class="num">{{ valuation.equity_value_per_share.toFixed(2) }} 元</td>
                    </tr>
                  </tbody>
                </table>
                <el-alert v-for="(n, i) in valuation.notes" :key="i" :title="n" type="warning" :closable="false" style="margin-top:8px" />
              </template>
            </div>
          </section>

          <section class="card" v-loading="aiLoading">
            <div class="card-head"><h2 class="card-title">AI 智能分析</h2></div>
            <div class="card-body">
              <div class="ai-form">
                <div class="ai-form-row">
                  <span class="ai-mode-label">分析模式</span>
                  <el-select v-model="aiReasoning" size="small" style="width:110px">
                    <el-option label="简洁" value="low" />
                    <el-option label="标准" value="medium" />
                    <el-option label="详细" value="high" />
                  </el-select>
                  <el-button type="primary" :loading="aiLoading" @click="fetchAIAnalysis">一键分析</el-button>
                </div>
                <div class="ai-hint">对公司财务指标进行智能诊断，输出五维评分、综合结论、行业分析与估值模型推荐。分析模式：简洁更快、标准均衡、详细更深入。</div>
              </div>
              <el-alert v-if="aiError" :title="aiError" type="error" :closable="false" style="margin-top:12px" />
              <template v-if="aiResult">
                <div class="ai-radar"><div ref="aiRadarRef" class="ai-radar-box"></div></div>
                <div class="ai-scores">
                  <div v-for="s in aiResult.scores" :key="s.dimension" class="ai-score-row">
                    <span class="ai-score-name">{{ s.dimension }}</span>
                    <el-progress :percentage="s.score" :stroke-width="12" :color="scoreColor(s.score)" style="flex:1" />
                    <span class="ai-score-comment">{{ s.comment }}</span>
                  </div>
                </div>
                <div class="ai-section">
                  <div class="ai-section-title">总体结论</div>
                  <p class="ai-text">{{ aiResult.conclusion }}</p>
                </div>
                <div class="ai-section">
                  <div class="ai-section-title">行业分析：{{ aiResult.industry.name }}</div>
                  <div class="ai-grid">
                    <div><span class="ai-k">前景</span><span class="ai-v">{{ aiResult.industry.prospect }}</span></div>
                    <div><span class="ai-k">竞争格局</span><span class="ai-v">{{ aiResult.industry.competition }}</span></div>
                    <div><span class="ai-k">应用方向</span><span class="ai-v">{{ aiResult.industry.application }}</span></div>
                  </div>
                </div>
                <div class="ai-section" v-if="aiResult.businesses && aiResult.businesses.length">
                  <div class="ai-section-title">业务板块分析</div>
                  <div class="ai-businesses">
                    <div v-for="b in aiResult.businesses" :key="b.name" class="ai-business">
                      <div class="ai-biz-head">
                        <span class="ai-biz-name">{{ b.name }}</span>
                        <span class="ai-biz-stage">{{ b.stage }}</span>
                        <span class="ai-biz-contribution">{{ b.contribution }}</span>
                      </div>
                      <div class="ai-biz-row"><span class="ai-k">前景</span><span class="ai-v">{{ b.prospect }}</span></div>
                      <div class="ai-biz-row"><span class="ai-k">风险</span><span class="ai-v">{{ b.risk }}</span></div>
                    </div>
                  </div>
                </div>
                <div class="ai-section">
                  <div class="ai-section-title">估值模型推荐</div>
                  <div class="ai-valuation">
                    <span class="ai-model">{{ aiResult.valuation.model_name }}</span>
                    <span class="ai-v">折现率 {{ fmtNum(aiResult.valuation.discount_rate) }}%</span>
                    <span v-for="p in aiResult.valuation.params" :key="p.key" class="ai-v">{{ p.label }} {{ fmtNum(p.value) }}</span>
                    <span v-if="aiResult.valuation.base_growth != null" class="ai-v">基准增速 {{ fmtNum(aiResult.valuation.base_growth) }}%</span>
                    <el-button size="small" type="primary" plain @click="applyAIValuation(aiResult.valuation)">套用到估值</el-button>
                  </div>
                  <p class="ai-text" v-if="aiResult.valuation.base_growth_note">{{ aiResult.valuation.base_growth_note }}</p>
                  <p class="ai-text" v-if="aiResult.valuation.adjust_rd">研发费用率 {{ fmtNum(aiResult.valuation.rd_ratio) }}%，研发投入占比较高，建议开启研发调整（点「套用到估值」会自动打开该开关）。</p>
                  <p class="ai-text" v-if="aiResult.valuation.volatile">增速波动剧烈：高增长期增长率已按基准增速的 50% 取值。</p>
                  <el-alert v-if="aiResult.valuation.warning" :title="aiResult.valuation.warning" type="warning" :closable="false" />
                  <p class="ai-text">{{ aiResult.valuation.rationale }}</p>
                </div>
                <div v-if="aiResult.usage" class="ai-usage">本次分析消耗约 {{ aiResult.usage.total_tokens }} tokens（输入 {{ aiResult.usage.prompt_tokens }} / 输出 {{ aiResult.usage.completion_tokens }}）</div>
              </template>
            </div>
          </section>
        </template>
      </main>
    </div>
  `,
});

app.use(ElementPlus);
app.mount('#app');
