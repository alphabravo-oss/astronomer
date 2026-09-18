/**
 * The canonical compositional table contract for operator routes.
 *
 * DataTable remains the default for sortable/filterable collections. This
 * contract is deliberately reserved for compact detail matrices whose cells
 * contain rich controls or nested values and therefore cannot be represented
 * as a row model without losing accessibility. Keeping the primitives here
 * prevents route modules from taking ownership of table markup and styling.
 */
export {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
