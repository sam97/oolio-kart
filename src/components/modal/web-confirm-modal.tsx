"use client";
import React from "react";
import { CustomModal } from "@/components/modal/modal";
import { CustomButton } from "@/components/button";
import Carts from "@/components/cart/carts";
import ImageContainer from "@/components/image";
import { createCartStore } from "@/stores/cart";

interface IWebConfirmationModal {
  title: string;
}

export default function WebConfirmationModal(props: IWebConfirmationModal) {
  const { title } = props;
  const { openWebModal, setOpenWebModal } = createCartStore();

  // TODO: call API to submit order
  const handleOnClick = () => {};

  return (
    <CustomModal
      open={openWebModal}
      okText="Start New Order"
      handleCancel={() => setOpenWebModal(false)}
    >
      <ImageContainer
        className="mt-s"
        src="/assets/success.png"
        height={30}
        width={30}
        alt="success-icon"
      />

      <p className="mt-s modalTitle">{title}</p>

      <p className=" label">We hope you enjoy your food!</p>

      <div className="px-m py-s overflow-y-auto max-h-[300px] scrollbar-hide bg-[#FBF7F4]">
        <Carts showSmallAmount={false} showDeleteIcon={false} />
      </div>

      <div className="mt-l">
        <CustomButton
          buttonText="Start New Order"
          onClickFunc={handleOnClick}
        />
      </div>
    </CustomModal>
  );
}
