/**
 * theme.ts —— 面板的暗色玻璃主题。
 *
 * 面板整体是"极暗的极光底图 + 半透明磨砂面板"。这套观感只有当 antd 自己的
 * 表面色也让出位置时才成立：默认主题会把卡片、表格、弹窗一律刷成不透明白，
 * 磨砂层后面就什么都没有了。所以这里不是给默认主题加一层 opacity，而是把
 * antd 的表面令牌整体换成半透明值，再由 style.css 补上 backdrop-filter。
 *
 * 令牌与 style.css 顶部的 :root 变量是同一套颜色的两种写法：CSS 变量给我们
 * 自己的类用，这里的令牌给 antd 组件用。改颜色要两处一起改。
 *
 * 这份配置有两个消费方：
 *   - App.vue 里的 <a-config-provider>，覆盖组件树内渲染的一切；
 *   - main.ts 里的 ConfigProvider.config()，覆盖 Modal.confirm / message 这类
 *     脱离组件树、自己挂一个根的静态调用。少了后者，确认框会是一块白板。
 */
import { theme as antTheme } from 'ant-design-vue'
import type { ThemeConfig } from 'ant-design-vue/es/config-provider/context'

/** 强调色。对白色文字的对比度 5.0:1，够 WCAG AA 正文。 */
const PRIMARY = '#4361ee'

/** 玻璃面板的填充与描边。数值贴着"看得见层次但不糊"的下限。 */
const GLASS_FILL = 'rgba(255, 255, 255, 0.06)'
const GLASS_BORDER = 'rgba(255, 255, 255, 0.16)'
const GLASS_BORDER_SOFT = 'rgba(255, 255, 255, 0.09)'

/**
 * 浮层（模态框、抽屉、下拉）比页面里的卡片更不透明。
 * 浮层下面压着的是整页内容而不是背景图，太透就会看到两层文字叠在一起。
 */
const GLASS_ELEVATED = 'rgba(19, 25, 44, 0.78)'

const TEXT = 'rgba(255, 255, 255, 0.92)'
const TEXT_SECONDARY = 'rgba(255, 255, 255, 0.72)'
const TEXT_TERTIARY = 'rgba(255, 255, 255, 0.55)'
/*
  0.48 而不是暗色算法默认的 0.25：这一档同时是输入框 placeholder 的颜色，
  默认值压在玻璃面板上只有 2.5:1 左右，离 AA 差得远。
*/
const TEXT_QUATERNARY = 'rgba(255, 255, 255, 0.48)'

/**
 * 一套系统字体栈，不引 webfont。
 *
 * 面板经常部署在没有外网出口的机器上，远程字体要么加载失败要么拖慢首屏；
 * 而 UI 上真正需要的只是"不是 Times New Roman"。
 */
const FONT_STACK = [
  'Inter',
  'system-ui',
  '-apple-system',
  'Segoe UI',
  'Roboto',
  'Helvetica Neue',
  'PingFang SC',
  'Hiragino Sans GB',
  'Microsoft YaHei',
  'sans-serif',
].join(', ')

export const glassTheme: ThemeConfig = {
  algorithm: antTheme.darkAlgorithm,
  token: {
    colorPrimary: PRIMARY,
    colorInfo: PRIMARY,
    colorSuccess: '#34d399',
    colorWarning: '#fbbf24',
    colorError: '#fb7185',
    colorLink: '#8ea6ff',
    /*
      antd 的键盘焦点环用的是 colorPrimaryBorder（见 es/style 的 genFocusOutline）。
      暗色算法从 #4361ee 推出来的那一档是很深的蓝，压在暗色玻璃面板上几乎看不出
      焦点落在哪儿。这里直接钉成一个亮色，让所有 antd 组件的焦点环一次性可见。
    */
    colorPrimaryBorder: '#8ea6ff',

    colorText: TEXT,
    colorTextHeading: TEXT,
    colorTextSecondary: TEXT_SECONDARY,
    colorTextDescription: TEXT_TERTIARY,
    colorTextTertiary: TEXT_TERTIARY,
    colorTextQuaternary: TEXT_QUATERNARY,

    // 页面底色交给 body 上的极光图，Layout 自己不要再刷一层。
    colorBgLayout: 'transparent',
    colorBgContainer: GLASS_FILL,
    colorBgElevated: GLASS_ELEVATED,
    colorBgSpotlight: 'rgba(24, 31, 54, 0.94)',
    colorBgMask: 'rgba(5, 8, 18, 0.6)',

    colorBorder: GLASS_BORDER,
    colorBorderSecondary: GLASS_BORDER_SOFT,
    colorSplit: GLASS_BORDER_SOFT,
    colorFillAlter: 'rgba(255, 255, 255, 0.05)',

    borderRadius: 12,
    borderRadiusLG: 18,
    borderRadiusSM: 8,
    controlHeight: 34,

    fontFamily: FONT_STACK,
    fontSize: 14,

    // 玻璃靠"薄边 + 大范围软阴影"撑出厚度，硬阴影会让它变回实心卡片。
    boxShadow: '0 18px 40px rgba(3, 6, 16, 0.45)',
    boxShadowSecondary: '0 24px 60px rgba(3, 6, 16, 0.55)',

    // wireframe 会把组件退回描边风格，与磨砂面板互相打架。
    wireframe: false,
  },
  components: {
    Layout: {
      colorBgHeader: 'transparent',
      colorBgBody: 'transparent',
      colorBgTrigger: 'rgba(255, 255, 255, 0.14)',
    },
    Menu: {
      colorItemBg: 'transparent',
      colorSubItemBg: 'transparent',
      colorItemText: TEXT_SECONDARY,
      colorItemTextHover: '#ffffff',
      colorItemTextSelected: '#ffffff',
      colorItemBgHover: 'rgba(255, 255, 255, 0.08)',
      colorItemBgSelected: 'rgba(67, 97, 238, 0.32)',
      colorActiveBarWidth: 0,
      radiusItem: 12,
      radiusSubMenuItem: 10,
      itemMarginInline: 10,
    },
    Card: {
      colorBgContainer: GLASS_FILL,
      borderRadiusLG: 20,
    },
    Table: {
      colorBgContainer: 'transparent',
      colorFillAlter: 'rgba(255, 255, 255, 0.06)',
      borderRadiusLG: 18,
    },
    Modal: {
      colorBgElevated: GLASS_ELEVATED,
      borderRadiusLG: 22,
    },
    Drawer: {
      colorBgElevated: 'rgba(15, 20, 38, 0.86)',
    },
    Tabs: {
      colorBgContainer: 'rgba(255, 255, 255, 0.08)',
      borderRadius: 12,
    },
    Input: {
      colorBgContainer: 'rgba(255, 255, 255, 0.07)',
    },
    InputNumber: {
      colorBgContainer: 'rgba(255, 255, 255, 0.07)',
    },
    Select: {
      colorBgContainer: 'rgba(255, 255, 255, 0.07)',
      colorBgElevated: GLASS_ELEVATED,
    },
    DatePicker: {
      colorBgContainer: 'rgba(255, 255, 255, 0.07)',
      colorBgElevated: GLASS_ELEVATED,
    },
    List: {
      colorBgContainer: 'transparent',
    },
    Alert: {
      borderRadiusLG: 14,
    },
    Tooltip: {
      colorBgSpotlight: 'rgba(24, 31, 54, 0.94)',
    },
    Progress: {
      colorText: TEXT,
    },
  },
}
