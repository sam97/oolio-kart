"use client";
import { BottomModal } from "@/components/modal/bottom-modal";
import { createCartStore } from "@/stores/cart";
import React from "react";

export default function MobileConfirmationModal() {
  const { openMobileModal, setOpenMobileModal } = createCartStore();
  return (
    <BottomModal
      setOpen={setOpenMobileModal}
      open={openMobileModal}
      onCloseAction={() => setOpenMobileModal(false)}
    >
      <h1> Carts Items</h1>
    </BottomModal>
  );
}
