const { createApp, ref, computed, nextTick } = Vue;

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
    function renderIndicatorChart() {
      if (!chartRef.value || !chartIndicator.value || !analysis.value) return;
      const it = chartIndicator.value;
      const years = analysis.value.years || [];
      const values = it.values || [];
      const unit = it.unit || '';
      const isYuan = unit === '元';

      // 同比增速（%）：首年/上期缺失/上期为0或负 → 无法计算，置 null
      const growth = values.map((v, i) => {
        if (i === 0) return null;
        const prev = values[i - 1];
        if (v == null || prev == null || prev === 0 || prev < 0) return null;
        return ((v - prev) / prev) * 100;
      });

      const fmtAxis = (v) => {
        if (isYuan) {
          const yi = v / 1e8;
          if (Math.abs(yi) >= 1) return yi.toFixed(1) + '亿';
          return (v / 1e4).toFixed(0) + '万';
        }
        return v;
      };
      const fmtVal = (v) => {
        if (v == null) return '—';
        if (isYuan) return fmtYi(v);
        if (unit === '%') return v.toFixed(2) + '%';
        if (unit === '倍') return v.toFixed(2) + ' 倍';
        return v.toFixed(2);
      };

      const el = chartRef.value;
      let chart = echarts.getInstanceByDom(el);
      if (chart) chart.dispose();
      chart = echarts.init(el);
      chart.setOption({
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
        legend: { data: [it.name, '同比增速'], top: 0, textStyle: { color: '#5d5151' } },
        grid: { left: 64, right: 64, top: 40, bottom: 32 },
        xAxis: { type: 'category', data: years.map(String), boundaryGap: true },
        yAxis: [
          {
            type: 'value',
            name: unit,
            nameTextStyle: { color: '#998c8c' },
            axisLabel: { formatter: fmtAxis },
            splitLine: { lineStyle: { type: 'dashed' } },
          },
          {
            type: 'value',
            name: '增速 %',
            nameTextStyle: { color: '#998c8c' },
            axisLabel: { formatter: '{value}%' },
            splitLine: { show: false },
          },
        ],
        series: [
          {
            name: it.name,
            type: 'bar',
            data: values.map(v => v == null ? null : v),
            itemStyle: { color: '#d64040', borderRadius: [3, 3, 0, 0] },
            barMaxWidth: 44,
          },
          {
            name: '同比增速',
            type: 'line',
            yAxisIndex: 1,
            data: growth,
            smooth: true,
            symbol: 'circle',
            symbolSize: 7,
            lineStyle: { width: 2, color: '#d97706' },
            itemStyle: { color: '#d97706' },
            connectNulls: false,
          },
        ],
      });
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
    function renderRadar() {
      if (!aiRadarRef.value || !aiResult.value) return;
      const scores = aiResult.value.scores || [];
      const el = aiRadarRef.value;
      let chart = echarts.getInstanceByDom(el);
      if (chart) chart.dispose();
      chart = echarts.init(el);
      chart.setOption({
        tooltip: {},
        radar: {
          indicator: scores.map(s => ({ name: s.dimension, max: 100 })),
          center: ['50%', '52%'],
          radius: '66%',
          splitArea: { areaStyle: { color: ['rgba(64,158,255,0.03)', 'rgba(64,158,255,0.06)'] } },
          axisName: { color: '#606266', fontSize: 13 },
        },
        series: [{
          type: 'radar',
          data: [{
            value: scores.map(s => s.score),
            name: '财务评分',
            symbolSize: 5,
            lineStyle: { color: '#d64040', width: 2 },
            itemStyle: { color: '#d64040' },
            areaStyle: { color: 'rgba(64,158,255,0.25)' },
          }],
        }],
      });
    }

    // 按评分档位取进度条颜色。
    function scoreColor(score) {
      if (score >= 90) return '#d64040'; // 优秀：红（A股红=强）
      if (score >= 75) return '#e8833a'; // 良好：橙
      if (score >= 60) return '#d9a240'; // 一般：琥珀
      return '#149a5e';                   // 较弱：绿（A股绿=弱）
    }

    // 数值显示：整数不带小数，其余保留两位。
    function fmtNum(v) {
      if (v == null || isNaN(v)) return '';
      return Number.isInteger(v) ? String(v) : v.toFixed(2);
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

    // R2 数值格式化：元 → 亿/万
    function fmtYi(v) {
      if (v == null || isNaN(v)) return '—';
      const yi = v / 1e8;
      if (Math.abs(yi) < 0.01) return (v / 1e4).toFixed(2) + '万';
      return yi.toFixed(2) + '亿';
    }

    // R3 分析指标数值格式化：按单位区分（元→亿/万、%→百分比）
    function fmtAnalysisVal(v, unit) {
      if (v == null || isNaN(v)) return '—';
      if (unit === '%') return v.toFixed(2) + '%';
      if (unit === '元') return fmtYi(v);
      return v.toFixed(2) + (unit ? ' ' + unit : '');
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

    // 同比涨跌色：A股红涨绿跌
    function yoyClass(values, i) {
      if (i === 0) return '';
      const prev = values[i - 1], cur = values[i];
      if (cur == null || prev == null || prev === 0 || prev < 0) return '';
      return (cur - prev) / prev >= 0 ? 'up' : 'down';
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

    return {
      code, loading, error, data, fetchData,
      financials, activeTab, startYear, endYear, yearOptions, finLoading,
      doSearch, pickCode, changeRange, fmtYi, yoyText, yoyClass, stmtYears, stmtGroups, hasStmtData,
      fmt,
      analysis, analysisLoading, analysisError, analysisActive, fmtAnalysisVal, indRowClass,
      tabTouchStart, tabTouchEnd,
      analysisStartYear, analysisEndYear, changeAnalysisRange,
      chartVisible, chartIndicator, chartRef, showIndicatorChart, renderIndicatorChart,
      valModels, valModel, valModelDesc, valDiscount, valG, valG1, valN1, valG2, valN2, valG3,
      valFCFModes, valFCFMode, valFCFYears, valAdjustRD,
      valuation, valLoading, valError, fetchValuation,
      aiLoading, aiError, aiResult, aiRadarRef, aiReasoning, fetchAIAnalysis, renderRadar,
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
                                  <div class="yoy" :class="yoyClass(it.values, i)">{{ yoyText(it.values, i) }}</div>
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
              <el-dialog v-model="chartVisible" :title="chartIndicator ? chartIndicator.name : ''" width="min(680px, 92vw)" destroy-on-close @opened="renderIndicatorChart">
                <div ref="chartRef" style="width:100%;height:380px"></div>
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
                      <el-input-number v-model="valDiscount" :min="0.1" :max="100" :step="0.5" :precision="1" style="width:110px" />
                      <span class="val-unit">%</span>
                    </div>

                    <div class="val-field" v-if="valModel === 'perpetual'">
                      <span class="val-label">永续增长率</span>
                      <el-input-number v-model="valG" :min="-50" :max="50" :step="0.5" :precision="1" style="width:110px" />
                      <span class="val-unit">%</span>
                    </div>

                    <template v-if="valModel === 'two_stage'">
                      <div class="val-field">
                        <span class="val-label">高增长期增长率</span>
                        <el-input-number v-model="valG1" :min="-50" :max="100" :step="0.5" :precision="1" style="width:110px" />
                        <span class="val-unit">%</span>
                      </div>
                      <div class="val-field">
                        <span class="val-label">高增长期年数</span>
                        <el-input-number v-model="valN1" :min="1" :max="50" :step="1" style="width:90px" />
                        <span class="val-unit">年</span>
                      </div>
                      <div class="val-field">
                        <span class="val-label">永续增长率</span>
                        <el-input-number v-model="valG2" :min="-50" :max="50" :step="0.5" :precision="1" style="width:110px" />
                        <span class="val-unit">%</span>
                      </div>
                    </template>

                    <template v-if="valModel === 'three_stage'">
                      <div class="val-field">
                        <span class="val-label">第一阶段增长率</span>
                        <el-input-number v-model="valG1" :min="-50" :max="100" :step="0.5" :precision="1" style="width:110px" />
                        <span class="val-unit">%</span>
                      </div>
                      <div class="val-field">
                        <span class="val-label">第一阶段年数</span>
                        <el-input-number v-model="valN1" :min="1" :max="50" :step="1" style="width:90px" />
                        <span class="val-unit">年</span>
                      </div>
                      <div class="val-field">
                        <span class="val-label">第二阶段增长率</span>
                        <el-input-number v-model="valG2" :min="-50" :max="100" :step="0.5" :precision="1" style="width:110px" />
                        <span class="val-unit">%</span>
                      </div>
                      <div class="val-field">
                        <span class="val-label">第二阶段年数</span>
                        <el-input-number v-model="valN2" :min="1" :max="50" :step="1" style="width:90px" />
                        <span class="val-unit">年</span>
                      </div>
                      <div class="val-field">
                        <span class="val-label">永续增长率</span>
                        <el-input-number v-model="valG3" :min="-50" :max="50" :step="0.5" :precision="1" style="width:110px" />
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
                      <el-input-number v-model="valFCFYears" :min="1" :max="10" :step="1" style="width:90px" />
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
                <div class="ai-radar"><div ref="aiRadarRef" style="width:100%;height:340px"></div></div>
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
                    <el-button size="small" type="primary" plain @click="applyAIValuation(aiResult.valuation)">套用到估值</el-button>
                  </div>
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
