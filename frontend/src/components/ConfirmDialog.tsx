import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";

export type ConfirmRequest = {
  title: string;
  description?: string;
  confirmText?: string;
  cancelText?: string;
  destructive?: boolean;
  action: () => void;
};

// ConfirmDialog replaces window.confirm: the native dialog steals focus,
// renders outside the app's theme, and cannot be styled; this keeps focus
// trapping and Escape handling consistent with the rest of the app.
export function ConfirmDialog({ request, onClose }: {
  request: ConfirmRequest | null;
  onClose: () => void;
}) {
  const destructive = request?.destructive ?? true;
  return (
    <AlertDialog
      open={!!request}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{request?.title}</AlertDialogTitle>
          {request?.description && (
            <AlertDialogDescription>{request.description}</AlertDialogDescription>
          )}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel asChild>
            <Button variant="outline">{request?.cancelText ?? "取消"}</Button>
          </AlertDialogCancel>
          <AlertDialogAction asChild>
            <Button
              variant={destructive ? "destructive" : "default"}
              onClick={() => {
                request?.action();
                onClose();
              }}
            >
              {request?.confirmText ?? "确认"}
            </Button>
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
