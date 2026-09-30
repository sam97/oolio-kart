"use client";
import React, { useEffect, useState } from "react";
import { Container } from "@components/container";
import { CustomButton } from "@/components/button";
import ImageContainer from "@/components/image";
import { createCartStore } from "@/stores/cart";
import Carts from "@/components/cart/carts";
import Products from "@/components/product/products";
import OrderConfirmaModal from "./order-confirm-modal";

function Home(props: any) {
  const { carts, getStorageCart, setOpenWebModal, setOpenMobileModal } =
    createCartStore();
  const [isMobile, setIsMobile] = useState(false);

  useEffect(() => {
    getStorageCart();
  }, []);

  // Detect Mobile or web mode
  useEffect(() => {
    const handleResize = () => {
      setIsMobile(window.innerWidth <= 768);
    };
    handleResize();
    window.addEventListener("resize", handleResize);
    return () => {
      window.removeEventListener("resize", handleResize);
    };
  }, []);

  const handleConfirmOrder = () => {
    if (!carts) return;
    if (!isMobile) {
      setOpenWebModal(true);

      return;
    } else {
      setOpenMobileModal(true);
    }
  };

  return (
    <Container>
      <div className="flex lg:flex-nowrap flex-wrap">
        <div className="flex flex-grow-[8] flex-col">
          <h1>Dessert</h1>
          {/* Product Section */}
          <Products />
        </div>

        {/* Cart Section */}
        <div
          className={`flex bg-white lg:ml-m lg:mt-0 mt-xxl ml-0 ${
            Object.keys(carts).length > 0 ? "max-h-[545px]" : "h-[300px]"
          } min-w-[395px]`}
        >
          <div className="p-l w-full flex-grow">
            {/* Carts */}
            <h2>{`Your Cart (${Object.keys(carts).length})`}</h2>
            <Carts />

            {/* Message & Confirm Button */}
            {Object.keys(carts).length > 0 && (
              <>
                <div className="mt-l flex justify-center items-center bg-grayBg">
                  <ImageContainer src="/assets/tree.png" alt="tree-icon" />
                  <div className="ml-xs py-xs">
                    <p className="txt">
                      This is a
                      <span className="text-[#837975] px-[3px]">
                        carbon-neutral
                      </span>
                      delivery
                    </p>
                  </div>
                </div>
                <div className="mt-l">
                  <CustomButton
                    buttonText="Confirm Order"
                    onClickFunc={handleConfirmOrder}
                  />
                </div>
              </>
            )}
          </div>
        </div>
      </div>

      {/* Confirmation Modal */}
      <OrderConfirmaModal isMobile={isMobile} />
    </Container>
  );
}
export default Home;
