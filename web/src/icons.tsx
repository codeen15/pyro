// Ant Design icons under the names the components use. Each takes an
// optional pixel size and is decorative unless given an aria-label.
import type { ComponentType, CSSProperties } from "react";
import {
  ApiOutlined,
  AppstoreAddOutlined,
  AppstoreOutlined,
  ArrowDownOutlined,
  ArrowUpOutlined,
  BookOutlined,
  BorderOutlined,
  CaretRightOutlined,
  CheckCircleOutlined,
  CheckOutlined,
  CloseCircleOutlined,
  CloseOutlined,
  ClusterOutlined,
  CodeOutlined,
  ControlOutlined,
  CopyOutlined,
  DashboardOutlined,
  DatabaseOutlined,
  DeleteOutlined,
  DiffOutlined,
  DisconnectOutlined,
  FallOutlined,
  RiseOutlined,
  SwapOutlined,
  DownloadOutlined,
  ExclamationCircleOutlined,
  ExportOutlined,
  EyeInvisibleOutlined,
  EyeOutlined,
  FileAddOutlined,
  FolderOpenOutlined,
  FolderOutlined,
  GlobalOutlined,
  ImportOutlined,
  KeyOutlined,
  LineChartOutlined,
  LinkOutlined,
  LoginOutlined,
  MinusOutlined,
  PaperClipOutlined,
  PlusOutlined,
  ReadOutlined,
  ReloadOutlined,
  RightOutlined,
  SafetyCertificateOutlined,
  SafetyOutlined,
  SaveOutlined,
  SendOutlined,
  SettingOutlined,
  TableOutlined,
  UndoOutlined,
  UploadOutlined,
  WarningOutlined,
  WifiOutlined,
} from "@ant-design/icons";

interface Props {
  size?: number;
  className?: string;
  style?: CSSProperties;
  "aria-label"?: string;
  "aria-hidden"?: boolean;
}

type AntIcon = ComponentType<{ className?: string; style?: CSSProperties; "aria-label"?: string; "aria-hidden"?: boolean; rotate?: number }>;

function wrap(Icon: AntIcon, rotate?: number) {
  return function PyroIcon({ size, className, style, ...aria }: Props) {
    const label = aria["aria-label"];
    return (
      <Icon
        className={className}
        rotate={rotate}
        style={size ? { fontSize: size, ...style } : style}
        aria-label={label}
        aria-hidden={label ? undefined : true}
      />
    );
  };
}

export const Activity = wrap(LineChartOutlined);
export const ArrowDown = wrap(ArrowDownOutlined);
export const ArrowUp = wrap(ArrowUpOutlined);
export const ArrowLeftRight = wrap(SwapOutlined);
export const Equal = wrap(MinusOutlined);
export const GitCompareArrows = wrap(DiffOutlined);
export const TrendingDown = wrap(FallOutlined);
export const TrendingUp = wrap(RiseOutlined);
export const ArrowDownLeft = wrap(ArrowDownOutlined, 45);
export const ArrowUpRight = wrap(ArrowUpOutlined, 45);
export const BookOpen = wrap(ReadOutlined);
export const Bookmark = wrap(BookOutlined);
export const BookmarkPlus = wrap(FileAddOutlined);
export const Check = wrap(CheckOutlined);
export const ChevronRight = wrap(RightOutlined);
export const CircleAlert = wrap(ExclamationCircleOutlined);
export const CircleCheck = wrap(CheckCircleOutlined);
export const CircleX = wrap(CloseCircleOutlined);
export const Cookie = wrap(DatabaseOutlined);
export const Copy = wrap(CopyOutlined);
export const Download = wrap(DownloadOutlined);
export const ExternalLink = wrap(ExportOutlined);
export const Eye = wrap(EyeOutlined);
export const EyeOff = wrap(EyeInvisibleOutlined);
export const FileInput = wrap(ImportOutlined);
export const FileUp = wrap(PaperClipOutlined);
export const Folder = wrap(FolderOutlined);
export const FolderOpen = wrap(FolderOpenOutlined);
export const Gauge = wrap(DashboardOutlined);
export const Globe = wrap(GlobalOutlined);
export const KeyRound = wrap(KeyOutlined);
export const Layers = wrap(AppstoreOutlined);
export const LogIn = wrap(LoginOutlined);
export const Minus = wrap(MinusOutlined);
export const Network = wrap(ClusterOutlined);
export const Play = wrap(CaretRightOutlined);
export const Plug = wrap(ApiOutlined);
export const PlugZap = wrap(LinkOutlined);
export const Unplug = wrap(DisconnectOutlined);
export const Plus = wrap(PlusOutlined);
export const Puzzle = wrap(AppstoreAddOutlined);
export const Radio = wrap(WifiOutlined);
export const RefreshCw = wrap(ReloadOutlined);
export const RotateCcw = wrap(UndoOutlined);
export const Save = wrap(SaveOutlined);
export const SendHorizontal = wrap(SendOutlined);
export const Settings2 = wrap(SettingOutlined);
export const ShieldAlert = wrap(SafetyOutlined);
export const ShieldCheck = wrap(SafetyCertificateOutlined);
export const SlidersHorizontal = wrap(ControlOutlined);
export const Square = wrap(BorderOutlined);
export const Table2 = wrap(TableOutlined);
export const Terminal = wrap(CodeOutlined);
export const Trash2 = wrap(DeleteOutlined);
export const TriangleAlert = wrap(WarningOutlined);
export const Upload = wrap(UploadOutlined);
export const X = wrap(CloseOutlined);

/** A small status dot (replaces an outlined circle icon). */
export function Circle({ className }: Props) {
  return <span className={`dot-icon ${className ?? ""}`} aria-hidden />;
}
