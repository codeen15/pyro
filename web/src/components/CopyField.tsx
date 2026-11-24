import { App as AntApp, Button, Input, Space } from "antd";
import { Copy } from "../icons";

/** Copies text to the clipboard, telling the user how it went. */
export function useCopy() {
  const { message } = AntApp.useApp();
  return (value: string) =>
    navigator.clipboard.writeText(value).then(
      () => message.success("Copied"),
      () => message.error("Couldn't copy; select it and copy"),
    );
}

/** A read-only field with a Copy button. */
export function CopyField({ label, value }: { label: string; value: string }) {
  const copy = useCopy();
  return (
    <Space.Compact block>
      <Input readOnly value={value} aria-label={label} onFocus={(e) => e.target.select()} />
      <Button icon={<Copy size={14} />} onClick={() => copy(value)}>
        Copy
      </Button>
    </Space.Compact>
  );
}
