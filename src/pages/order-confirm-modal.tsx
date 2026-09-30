"use client";
import MobileConfirmationModal from "@/components/modal/mobile-confirm-modal";
import WebConfirmationModal from "@/components/modal/web-confirm-modal";
import React from "react";

interface IOrderConfirmaModal {
  isMobile: boolean;
}

export default function OrderConfirmaModal(props: IOrderConfirmaModal) {
  const { isMobile } = props;
  {
    !isMobile && <WebConfirmationModal title="Order Confirmed" />;
  }

  {
    isMobile && <MobileConfirmationModal />;
  }
  return null;
}
