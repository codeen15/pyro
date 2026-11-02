import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { App, Button, Dropdown, Input, Modal, type MenuProps } from "antd";
import { parseDocument } from "yaml";
import { api } from "../api";
import { useApp } from "../store";
import { slugify } from "../loadProfiles";
import { Copy, MoreVertical, Pencil, Settings2, Trash2 } from "../icons";

/** A copy of a plan's YAML with a new title (comments are kept). */
function retitle(yaml: string, title: string) {
  const doc = parseDocument(yaml);
  doc.set("name", title);
  return doc.toString();
}

async function uniqueName(base: string, except?: string) {
  const taken = new Set((await api.loadTests()).map((t) => t.name).filter((n) => n !== except));
  let name = base || "load-test";
  for (let i = 2; taken.has(name); i++) name = `${base}-${i}`;
  return name;
}

/** Rename, duplicate and delete for load test plans (loadtests/<name>.load.yaml). */
export function useLoadTestActions() {
  const qc = useQueryClient();
  const { modal, message } = App.useApp();
  const select = useApp((s) => s.select);
  const [naming, setNaming] = useState<{ mode: "rename" | "duplicate"; name: string; title: string } | null>(null);
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);

  // A removed plan's query is dropped rather than refetched (it would 404).
  const forget = (name: string) => qc.removeQueries({ queryKey: ["loadtest", name] });

  const submit = async () => {
    if (!naming || !value.trim()) return;
    setBusy(true);
    try {
      const title = value.trim();
      const src = await api.loadTest(naming.name);
      const next = await uniqueName(slugify(title), naming.mode === "rename" ? naming.name : undefined);
      await api.saveLoadTest(next, retitle(src.yaml ?? "", title));
      select(`load:${next}`);
      setNaming(null);
      if (naming.mode === "rename" && next !== naming.name) {
        await api.deleteLoadTest(naming.name);
        forget(naming.name);
      } else {
        await qc.invalidateQueries({ queryKey: ["loadtest", next] });
      }
      await qc.invalidateQueries({ queryKey: ["loadtests"] });
    } catch (e) {
      message.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const remove = (name: string, title: string) =>
    modal.confirm({
      title: `Delete the load test “${title}”?`,
      content: `This deletes loadtests/${name}.load.yaml. Saved runs stay in the history.`,
      okText: "Delete",
      okButtonProps: { danger: true },
      cancelText: "Keep it",
      onOk: async () => {
        try {
          await api.deleteLoadTest(name);
        } catch (e) {
          message.error((e as Error).message);
          throw e;
        }
        if (useApp.getState().selected === `load:${name}`) select(null);
        forget(name);
        await qc.invalidateQueries({ queryKey: ["loadtests"] });
        message.success(`Deleted ${title}`);
      },
    });

  const open = (mode: "rename" | "duplicate", name: string, title: string) => {
    setValue(mode === "rename" ? title : `${title} copy`);
    setNaming({ mode, name, title });
  };

  const dialog = (
    <Modal
      title={naming?.mode === "rename" ? `Rename “${naming.title}”` : `Duplicate “${naming?.title ?? ""}”`}
      open={!!naming}
      okText={naming?.mode === "rename" ? "Rename" : "Duplicate"}
      okButtonProps={{ disabled: !value.trim() }}
      confirmLoading={busy}
      onOk={submit}
      onCancel={() => setNaming(null)}
      destroyOnHidden
    >
      <label className="kind-name">
        <span className="field-label">Name</span>
        <Input autoFocus aria-label="Load test name" value={value} onChange={(e) => setValue(e.target.value)} onPressEnter={submit} />
      </label>
      <p className="muted small">Saved as loadtests/{slugify(value.trim()) || "…"}.load.yaml.</p>
    </Modal>
  );

  const items = (name: string, title: string, withOpen: boolean): NonNullable<MenuProps["items"]> => [
    ...(withOpen ? [{ key: "edit", icon: <Settings2 size={14} />, label: `Edit ${title}`, onClick: () => select(`load:${name}`) }] : []),
    { key: "rename", icon: <Pencil size={14} />, label: `Rename ${title}`, onClick: () => open("rename", name, title) },
    { key: "duplicate", icon: <Copy size={14} />, label: `Duplicate ${title}`, onClick: () => open("duplicate", name, title) },
    { type: "divider" as const },
    { key: "delete", icon: <Trash2 size={14} />, danger: true, label: `Delete ${title}`, onClick: () => remove(name, title) },
  ];

  return { dialog, items, remove };
}

/** ⋮ menu for a load test (sidebar rows and the plan's header). */
export function LoadTestMenu({ name, title, inRail }: { name: string; title: string; inRail?: boolean }) {
  const { dialog, items } = useLoadTestActions();
  return (
    <>
      <Dropdown trigger={["click"]} menu={{ items: items(name, title, !!inRail) }}>
        <Button type={inRail ? "text" : "default"} icon={<MoreVertical size={14} />} aria-label={`More actions for ${title}`} />
      </Dropdown>
      {dialog}
    </>
  );
}
