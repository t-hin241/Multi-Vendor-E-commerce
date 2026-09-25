import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

export function StatusFilter({
  status,
  onChange,
  options,
}: {
  status: string;
  onChange: (s: string) => void;
  options: readonly string[];
}) {
  // Radix Select can't represent an empty-string item value (that's reserved
  // for "no selection"), so "" (this filter's "All" option) is mapped to a
  // sentinel for the Select itself and translated back on change -- the
  // `onChange(s)` contract callers rely on (empty string = All) is unchanged.
  return (
    <Select
      value={status || "__all__"}
      onValueChange={(next) => onChange(next === "__all__" ? "" : next)}
    >
      <SelectTrigger className="w-44">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map((s) => (
          <SelectItem key={s || "__all__"} value={s || "__all__"}>
            {s === "" ? "All" : s.replace(/_/g, " ")}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
