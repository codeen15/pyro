import { useState } from "react";
import { Button, Empty, Input, Table, Tag, Tooltip } from "antd";
import { ChevronRight, Trash2 } from "../icons";
import type { Example } from "../api";

// Saved responses for a request: documentation you can share through the
// workspace (and Postman exports). "Save as example" on a response adds one.

function statusColor(status: number) {
  return status >= 500 ? "error" : status >= 400 ? "warning" : status >= 300 ? "processing" : "success";
}

export function ExamplesEditor({ examples, onChange }: { examples: Example[]; onChange: (e: Example[]) => void }) {
  const [open, setOpen] = useState<number | null>(examples.length === 1 ? 0 : null);
  if (examples.length === 0) {
    return (
      <Empty
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        description={
          <>
            No examples yet. Send the request and use <strong>Save as example</strong> on the response to keep one here.
          </>
        }
      />
    );
  }
  const update = (i: number, patch: Partial<Example>) => onChange(examples.map((e, j) => (j === i ? { ...e, ...patch } : e)));
  return (
    <ul className="examples">
      {examples.map((ex, i) => (
        <li key={i} className={`example ${open === i ? "is-open" : ""}`}>
          <div className="example-head">
            <Button
              type="text"
              icon={<ChevronRight size={12} className={`chev ${open === i ? "open" : ""}`} />}
              onClick={() => setOpen(open === i ? null : i)}
              aria-expanded={open === i}
              aria-label={`Show example ${ex.name}`}
            />
            {ex.status ? (
              <Tag color={statusColor(ex.status)} className="status-tag">
                {ex.status}
              </Tag>
            ) : null}
            <Input value={ex.name} onChange={(e) => update(i, { name: e.target.value })} aria-label="Example name" />
            <Tooltip title="Delete example">
              <Button type="text" icon={<Trash2 size={13} />} onClick={() => onChange(examples.filter((_, j) => j !== i))} aria-label={`Delete example ${ex.name}`} />
            </Tooltip>
          </div>
          {open === i && (
            <div className="example-body">
              {(ex.headers?.length ?? 0) > 0 && (
                <Table
                  size="small"
                  bordered
                  pagination={false}
                  showHeader={false}
                  rowKey="row"
                  dataSource={ex.headers!.map((h, k) => ({ ...h, row: k }))}
                  columns={[
                    { key: "key", dataIndex: "key", className: "mono example-hkey" },
                    { key: "value", dataIndex: "value", className: "mono" },
                  ]}
                />
              )}
              <Input.TextArea
                className="code-input"
                value={ex.body ?? ""}
                onChange={(e) => update(i, { body: e.target.value })}
                spellCheck={false}
                autoSize={{ minRows: 8, maxRows: 30 }}
                aria-label="Example body"
              />
            </div>
          )}
        </li>
      ))}
    </ul>
  );
}
