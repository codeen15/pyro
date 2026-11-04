// Hugeicons (free set, stroke style) under the names the components use.
// Each takes an optional pixel size (default 1em, so it follows the text
// like antd's own icons) and is decorative unless given an aria-label.
import type { CSSProperties } from "react";
import { HugeiconsIcon, type IconSvgElement } from "@hugeicons/react";
import {
  Activity01Icon,
  Alert02Icon,
  AlertCircleIcon,
  ArrowDown01Icon,
  ArrowDown02Icon,
  ArrowDownLeft02Icon,
  ArrowLeftRightIcon,
  ArrowRight01Icon,
  ArrowUp02Icon,
  ArrowUpRight02Icon,
  Attachment01Icon,
  BookOpen01Icon,
  Bookmark02Icon,
  BookmarkAdd02Icon,
  Briefcase01Icon,
  Cancel01Icon,
  CancelCircleIcon,
  ChartDecreaseIcon,
  ChartIncreaseIcon,
  CheckmarkCircle02Icon,
  CommandLineIcon,
  CookieIcon,
  Copy01Icon,
  DashboardSpeed01Icon,
  Delete02Icon,
  Download04Icon,
  EqualSignIcon,
  FileImportIcon,
  FloppyDiskIcon,
  Folder01Icon,
  FolderAddIcon,
  FolderOpenIcon,
  GitCompareIcon,
  Globe02Icon,
  HierarchyIcon,
  Key01Icon,
  Layers01Icon,
  Link01Icon,
  LinkSquare02Icon,
  LockPasswordIcon,
  Login01Icon,
  Logout01Icon,
  MinusSignIcon,
  MoreVerticalIcon,
  PencilEdit02Icon,
  PlayIcon,
  Plug01Icon,
  PlusSignIcon,
  PuzzleIcon,
  RefreshIcon,
  RotateLeft01Icon,
  Search01Icon,
  SecurityCheckIcon,
  SecurityWarningIcon,
  SentIcon,
  Settings02Icon,
  SlidersHorizontalIcon,
  SquareIcon,
  Table01Icon,
  Tick02Icon,
  Unlink04Icon,
  Upload04Icon,
  UserCircleIcon,
  UserGroupIcon,
  ViewIcon,
  ViewOffSlashIcon,
  Wifi01Icon,
} from "@hugeicons/core-free-icons";

interface Props {
  size?: number;
  className?: string;
  style?: CSSProperties;
  "aria-label"?: string;
  "aria-hidden"?: boolean;
}

function wrap(icon: IconSvgElement) {
  return function PyroIcon({ size, className, style, ...aria }: Props) {
    const label = aria["aria-label"];
    return (
      <HugeiconsIcon
        icon={icon}
        size={size ?? "1em"}
        strokeWidth={1.75}
        className={className ? `hi ${className}` : "hi"}
        style={style}
        role={label ? "img" : undefined}
        aria-label={label}
        aria-hidden={label ? undefined : true}
      />
    );
  };
}

export const Activity = wrap(Activity01Icon);
export const ArrowDown = wrap(ArrowDown02Icon);
export const ArrowUp = wrap(ArrowUp02Icon);
export const ArrowLeftRight = wrap(ArrowLeftRightIcon);
export const ArrowDownLeft = wrap(ArrowDownLeft02Icon);
export const ArrowUpRight = wrap(ArrowUpRight02Icon);
export const BookOpen = wrap(BookOpen01Icon);
export const Bookmark = wrap(Bookmark02Icon);
export const BookmarkPlus = wrap(BookmarkAdd02Icon);
export const Briefcase = wrap(Briefcase01Icon);
export const Check = wrap(Tick02Icon);
export const ChevronDown = wrap(ArrowDown01Icon);
export const ChevronRight = wrap(ArrowRight01Icon);
export const CircleAlert = wrap(AlertCircleIcon);
export const CircleCheck = wrap(CheckmarkCircle02Icon);
export const CircleX = wrap(CancelCircleIcon);
export const Cookie = wrap(CookieIcon);
export const Copy = wrap(Copy01Icon);
export const Download = wrap(Download04Icon);
export const Equal = wrap(EqualSignIcon);
export const ExternalLink = wrap(LinkSquare02Icon);
export const Eye = wrap(ViewIcon);
export const EyeOff = wrap(ViewOffSlashIcon);
export const FileInput = wrap(FileImportIcon);
export const FileUp = wrap(Attachment01Icon);
export const Folder = wrap(Folder01Icon);
export const FolderOpen = wrap(FolderOpenIcon);
export const FolderPlus = wrap(FolderAddIcon);
export const Gauge = wrap(DashboardSpeed01Icon);
export const GitCompareArrows = wrap(GitCompareIcon);
export const Globe = wrap(Globe02Icon);
export const KeyRound = wrap(Key01Icon);
export const Layers = wrap(Layers01Icon);
export const LockKey = wrap(LockPasswordIcon);
export const LogIn = wrap(Login01Icon);
export const LogOut = wrap(Logout01Icon);
export const Minus = wrap(MinusSignIcon);
export const MoreVertical = wrap(MoreVerticalIcon);
export const Network = wrap(HierarchyIcon);
export const Pencil = wrap(PencilEdit02Icon);
export const Play = wrap(PlayIcon);
export const Plug = wrap(Plug01Icon);
export const PlugZap = wrap(Link01Icon);
export const Unplug = wrap(Unlink04Icon);
export const Plus = wrap(PlusSignIcon);
export const Puzzle = wrap(PuzzleIcon);
export const Radio = wrap(Wifi01Icon);
export const RefreshCw = wrap(RefreshIcon);
export const RotateCcw = wrap(RotateLeft01Icon);
export const Save = wrap(FloppyDiskIcon);
export const Search = wrap(Search01Icon);
export const SendHorizontal = wrap(SentIcon);
export const Settings2 = wrap(Settings02Icon);
export const ShieldAlert = wrap(SecurityWarningIcon);
export const ShieldCheck = wrap(SecurityCheckIcon);
export const SlidersHorizontal = wrap(SlidersHorizontalIcon);
export const Square = wrap(SquareIcon);
export const Table2 = wrap(Table01Icon);
export const Terminal = wrap(CommandLineIcon);
export const Trash2 = wrap(Delete02Icon);
export const TrendingDown = wrap(ChartDecreaseIcon);
export const TrendingUp = wrap(ChartIncreaseIcon);
export const TriangleAlert = wrap(Alert02Icon);
export const Upload = wrap(Upload04Icon);
export const UserCircle = wrap(UserCircleIcon);
export const Users = wrap(UserGroupIcon);
export const X = wrap(Cancel01Icon);

/** A small status dot (replaces an outlined circle icon). */
export function Circle({ className }: Props) {
  return <span className={`dot-icon ${className ?? ""}`} aria-hidden />;
}
